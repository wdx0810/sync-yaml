package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/configmap-sync/configmap-sync/internal/diff"
	"github.com/configmap-sync/configmap-sync/internal/gitlab"
	"github.com/configmap-sync/configmap-sync/internal/history"
	"github.com/configmap-sync/configmap-sync/internal/k8s/cleaner"
	k8sdynamic "github.com/configmap-sync/configmap-sync/internal/k8s/dynamic"
	"github.com/configmap-sync/configmap-sync/internal/k8s/gvr"
	generic "github.com/configmap-sync/configmap-sync/internal/parser/generic"
	"github.com/configmap-sync/configmap-sync/internal/path"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

// GenericSyncer handles sync operations for any K8s resource type.
type GenericSyncer struct {
	parser       generic.Parser
	gvrResolver  gvr.Resolver
	cleaner      cleaner.Cleaner
	differ       diff.Differ
	pathProvider path.Provider
	logger       *slog.Logger
}

// NewGenericSyncer creates a new generic syncer.
func NewGenericSyncer(resolver gvr.Resolver) *GenericSyncer {
	c := cleaner.NewCleaner()
	return &GenericSyncer{
		parser:       generic.NewParser(resolver),
		gvrResolver:  resolver,
		cleaner:      c,
		differ:       diff.NewDiffer(c),
		pathProvider: path.NewProvider(),
		logger:       slog.Default().With("component", "generic-syncer"),
	}
}

// PendingChange describes a single resource change planned by a forward sync.
// It carries everything needed to re-apply the change after user approval,
// without a second round-trip to GitLab.
type PendingChange struct {
	// Identification
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Action    string `json:"action"` // "created" or "updated"

	// Diff for user review.
	OldYAML string `json:"oldYAML"`
	NewYAML string `json:"newYAML"`

	// Internal state needed to apply after approval.
	APIVersion string `json:"apiVersion"`
	Namespaced bool   `json:"namespaced"`
	RawYAML    string `json:"-"` // not sent to client; used server-side only
}

// PreviewForward builds a list of changes that would be applied, without touching K8s.
func (s *GenericSyncer) PreviewForward(ctx context.Context, gc gitlab.Client, dc k8sdynamic.Client, source *store.GitLabSource, target *store.K8sTarget, resourceTypes []string) ([]PendingChange, *SyncResultInfo, error) {
	info := &SyncResultInfo{}
	var pending []PendingChange

	files, err := gc.FetchFiles(ctx, source.Path)
	if err != nil {
		info.Errors = append(info.Errors, fmt.Sprintf("fetch files: %v", err))
		return nil, info, err
	}

	for _, f := range files {
		resources, err := s.parser.ParseMulti(f.Content)
		if err != nil {
			info.Skipped++
			continue
		}
		for _, res := range resources {
			info.Total++

			if !s.matchesResourceTypes(res.Kind, resourceTypes) {
				info.Skipped++
				info.SkippedNames = append(info.SkippedNames, fmt.Sprintf("%s/%s (%s filtered)", res.Namespace, res.Name, res.Kind))
				continue
			}

			ns := res.Namespace
			if ns == "" {
				ns = target.Namespace
			}
			if res.Namespaced {
				res.Object.SetNamespace(ns)
			}
			res.Object.SetResourceVersion("")
			res.Object.SetManagedFields(nil)

			existing, getErr := dc.Get(ctx, ns, res.GVR, res.Name)
			if getErr == nil {
				if diff.IsSameContent(s.cleaner, existing, res.Object) {
					info.Skipped++
					info.SkippedNames = append(info.SkippedNames, fmt.Sprintf("%s/%s (%s)", ns, res.Name, res.Kind))
					continue
				}
			}

			action := "updated"
			var oldYAML string
			if getErr != nil {
				action = "created"
			}
			if existing != nil {
				cleanOld := s.cleaner.Clean(existing)
				if b, e := s.parser.Print(&generic.Resource{Object: cleanOld}); e == nil {
					oldYAML = string(b)
				}
			}
			// Clean the GitLab side too, so the diff view is symmetric.
			// Without this, fields like `progressDeadlineSeconds: 600`,
			// `creationTimestamp: null`, `imagePullPolicy: IfNotPresent` show up
			// on the right pane (cleaner skipped) but not the left (cleaner ran),
			// producing a confusing fake diff. The unmodified GitLab YAML is
			// preserved in RawYAML for actual application.
			cleanNew := s.cleaner.Clean(res.Object)
			newYAMLBytes, _ := s.parser.Print(&generic.Resource{Object: cleanNew})
			newYAML := string(newYAMLBytes)

			// Keep the original (uncleaned) YAML for Apply, so we send the user's
			// authoritative GitLab YAML to K8s rather than our normalized version.
			rawYAMLBytes, _ := s.parser.Print(res)
			rawYAML := string(rawYAMLBytes)

			pending = append(pending, PendingChange{
				Kind:       res.Kind,
				Namespace:  ns,
				Name:       res.Name,
				Action:     action,
				OldYAML:    oldYAML,
				NewYAML:    newYAML,
				APIVersion: res.GVR.Group + "/" + res.GVR.Version,
				Namespaced: res.Namespaced,
				RawYAML:    rawYAML,
			})
		}
	}

	return pending, info, nil
}

// ApplyChanges applies a pre-approved list of changes to K8s.
func (s *GenericSyncer) ApplyChanges(ctx context.Context, dc k8sdynamic.Client, changes []PendingChange) *SyncResultInfo {
	info := &SyncResultInfo{Total: len(changes)}

	for _, ch := range changes {
		// Re-parse the YAML to rebuild the resource object.
		resources, err := s.parser.ParseMulti([]byte(ch.RawYAML))
		if err != nil || len(resources) == 0 {
			info.Failed++
			info.FailedNames = append(info.FailedNames, fmt.Sprintf("%s/%s (%s)", ch.Namespace, ch.Name, ch.Kind))
			parseErr := "empty document"
			if err != nil {
				parseErr = err.Error()
			}
			info.Errors = append(info.Errors, fmt.Sprintf("parse %s/%s/%s: %s", ch.Namespace, ch.Kind, ch.Name, parseErr))
			s.logger.Error("apply parse failed", "ns", ch.Namespace, "kind", ch.Kind, "name", ch.Name, "error", parseErr)
			continue
		}
		res := resources[0]

		// If the parser couldn't resolve the GVR (builtin mapping miss + nil discovery),
		// res.GVR.Resource is empty and dynamic client calls will fail with an
		// opaque error. Surface this clearly.
		if res.GVR.Resource == "" {
			info.Failed++
			info.FailedNames = append(info.FailedNames, fmt.Sprintf("%s/%s (%s)", ch.Namespace, ch.Name, ch.Kind))
			info.Errors = append(info.Errors, fmt.Sprintf("%s/%s/%s: 无法解析资源类型 (apiVersion=%s)", ch.Namespace, ch.Kind, ch.Name, ch.APIVersion))
			s.logger.Error("apply gvr unresolved", "ns", ch.Namespace, "kind", ch.Kind, "name", ch.Name, "apiVersion", ch.APIVersion)
			continue
		}

		ns := ch.Namespace
		if res.Namespaced {
			res.Object.SetNamespace(ns)
		}
		res.Object.SetResourceVersion("")
		res.Object.SetManagedFields(nil)

		s.logger.Info("applying", "ns", ns, "kind", ch.Kind, "name", ch.Name, "gvr", res.GVR.String())
		if err := dc.Apply(ctx, ns, res.GVR, res.Object); err != nil {
			info.Failed++
			info.FailedNames = append(info.FailedNames, fmt.Sprintf("%s/%s (%s)", ns, ch.Name, ch.Kind))
			info.Errors = append(info.Errors, fmt.Sprintf("%s/%s/%s: %v", ns, ch.Kind, ch.Name, err))
			info.Details = append(info.Details, history.ChangeDetail{
				Name: ch.Name, Namespace: ns, Kind: ch.Kind, Action: "failed", Error: err.Error(),
			})
			s.logger.Error("apply failed", "ns", ns, "kind", ch.Kind, "name", ch.Name, "error", err)
			continue
		}
		info.Synced++
		info.SyncedNames = append(info.SyncedNames, fmt.Sprintf("%s/%s (%s)", ns, ch.Name, ch.Kind))
		info.Details = append(info.Details, history.ChangeDetail{
			Name: ch.Name, Namespace: ns, Kind: ch.Kind, Action: ch.Action,
			OldYAML: ch.OldYAML, NewYAML: ch.NewYAML,
		})
	}
	return info
}

// ForwardSync syncs resources from GitLab to K8s.
func (s *GenericSyncer) ForwardSync(ctx context.Context, gc gitlab.Client, dc k8sdynamic.Client, source *store.GitLabSource, target *store.K8sTarget, resourceTypes []string) *SyncResultInfo {
	info := &SyncResultInfo{}

	files, err := gc.FetchFiles(ctx, source.Path)
	if err != nil {
		info.Errors = append(info.Errors, fmt.Sprintf("fetch files: %v", err))
		return info
	}

	info.Total = 0
	for _, f := range files {
		resources, err := s.parser.ParseMulti(f.Content)
		if err != nil {
			info.Skipped++
			continue
		}

		for _, res := range resources {
			info.Total++

			// Filter by resource types.
			if !s.matchesResourceTypes(res.Kind, resourceTypes) {
				info.Skipped++
				info.SkippedNames = append(info.SkippedNames, fmt.Sprintf("%s/%s (%s filtered)", res.Namespace, res.Name, res.Kind))
				continue
			}

			ns := res.Namespace
			if ns == "" {
				ns = target.Namespace
			}

			// Ensure the object's namespace matches the target namespace for Apply.
			if res.Namespaced {
				res.Object.SetNamespace(ns)
			}

			// Remove resourceVersion to avoid conflicts on update.
			res.Object.SetResourceVersion("")
			// Remove managedFields to keep the apply payload clean.
			res.Object.SetManagedFields(nil)

			// Check if resource already exists with same content.
			existing, getErr := dc.Get(ctx, ns, res.GVR, res.Name)
			if getErr == nil {
				if diff.IsSameContent(s.cleaner, existing, res.Object) {
					info.Skipped++
					info.SkippedNames = append(info.SkippedNames, fmt.Sprintf("%s/%s (%s)", ns, res.Name, res.Kind))
					continue
				}
			}

			// Apply resource.
			if err := dc.Apply(ctx, ns, res.GVR, res.Object); err != nil {
				info.Failed++
				info.FailedNames = append(info.FailedNames, fmt.Sprintf("%s/%s (%s)", ns, res.Name, res.Kind))
				info.Errors = append(info.Errors, fmt.Sprintf("%s/%s/%s: %v", ns, res.Kind, res.Name, err))
				info.Details = append(info.Details, history.ChangeDetail{
					Name: res.Name, Namespace: ns, Action: "failed", Error: err.Error(),
				})
				continue
			}

			info.Synced++
			info.SyncedNames = append(info.SyncedNames, fmt.Sprintf("%s/%s (%s)", ns, res.Name, res.Kind))

			// Record diff.
			action := "updated"
			var oldYAML, newYAML string
			if getErr != nil {
				action = "created"
			}
			if existing != nil {
				cleanOld := s.cleaner.Clean(existing)
				if b, e := s.parser.Print(&generic.Resource{Object: cleanOld}); e == nil {
					oldYAML = string(b)
				}
			}
			if b, e := s.parser.Print(res); e == nil {
				newYAML = string(b)
			}
			info.Details = append(info.Details, history.ChangeDetail{
				Name: res.Name, Namespace: ns, Action: action,
				OldYAML: oldYAML, NewYAML: newYAML,
			})
		}
	}

	return info
}

// ReverseSync syncs resources from K8s to GitLab.
func (s *GenericSyncer) ReverseSync(ctx context.Context, gc gitlab.Client, dc k8sdynamic.Client, source *store.GitLabSource, target *store.K8sTarget, resourceTypes []string) *SyncResultInfo {
	info := &SyncResultInfo{}

	namespaces := strings.Split(target.Namespace, ",")
	for i := range namespaces {
		namespaces[i] = strings.TrimSpace(namespaces[i])
	}

	// Determine which GVRs to list.
	gvrs := s.resolveResourceTypes(resourceTypes)

	// Fetch existing GitLab files for comparison.
	existingFiles, _ := gc.FetchFiles(ctx, source.Path)
	existingContent := make(map[string]string) // path -> yaml content
	for _, f := range existingFiles {
		existingContent[f.Path] = string(f.Content)
	}

	// Collect all files to commit in one batch.
	type pendingFile struct {
		path    string
		content []byte
		detail  history.ChangeDetail
	}
	var pending []pendingFile

	for _, gvrInfo := range gvrs {
		kind := gvrInfo.Kind
		plural := gvr.KindToPlural(kind)

		// For cluster-scoped resources, list once globally (namespace empty).
		// For namespaced resources, list per namespace.
		listNamespaces := namespaces
		if !gvrInfo.Namespaced {
			listNamespaces = []string{""}
		}

		for _, ns := range listNamespaces {
			if gvrInfo.Namespaced && ns == "" {
				continue
			}

			resources, err := dc.List(ctx, ns, gvrInfo.GVR.GVR, "")
			if err != nil {
				s.logger.Warn("list failed", "gvr", gvrInfo.GVR, "ns", ns, "error", err)
				continue
			}

			for _, obj := range resources {
				name := obj.GetName()

				// Skip K8s-managed resources that should never be synced
				// (auto-created bookkeeping, controller-owned children, etc.).
				if shouldSkipResource(kind, ns, name, obj) {
					continue
				}

				info.Total++

				// Clean runtime fields.
				cleaned := s.cleaner.Clean(obj)
				yamlBytes, err := s.parser.Print(&generic.Resource{Object: cleaned})
				if err != nil {
					info.Skipped++
					continue
				}

				// Generate file path. For cluster-scoped resources the namespace
				// segment is replaced by "_cluster" (handled inside ResourcePath).
				filePath := s.pathProvider.ResourcePath(
					strings.TrimPrefix(source.Path, "/"),
					ns, plural, name, gvrInfo.Namespaced,
				)

				// Compare with existing.
				var oldYAML string
				action := "created"
				if existing, ok := existingContent[filePath]; ok {
					if strings.TrimSpace(existing) == strings.TrimSpace(string(yamlBytes)) {
						info.Skipped++
						continue
					}
					oldYAML = existing
					action = "updated"
				}

				pending = append(pending, pendingFile{
					path:    filePath,
					content: yamlBytes,
					detail: history.ChangeDetail{
						Name: name, Namespace: ns, Kind: kind, Action: action,
						OldYAML: oldYAML, NewYAML: string(yamlBytes),
					},
				})
			}
		}
	}

	// Commit all files in a single batch.
	if len(pending) > 0 {
		actions := make([]gitlab.FileCommitAction, len(pending))
		for i, p := range pending {
			actions[i] = gitlab.FileCommitAction{Path: p.path, Content: p.content}
		}

		commitMsg := fmt.Sprintf("Sync from K8s: %d resource(s)", len(pending))
		if err := gc.CommitFiles(ctx, actions, commitMsg); err != nil {
			// If batch commit fails, mark all as failed.
			for _, p := range pending {
				info.Failed++
				info.FailedNames = append(info.FailedNames, fmt.Sprintf("%s/%s", p.detail.Namespace, p.detail.Name))
				p.detail.Action = "failed"
				p.detail.Error = err.Error()
				info.Details = append(info.Details, p.detail)
			}
			info.Errors = append(info.Errors, fmt.Sprintf("commit failed: %v", err))
			s.logger.Error("batch commit failed", "count", len(pending), "error", err)
		} else {
			for _, p := range pending {
				info.Synced++
				info.SyncedNames = append(info.SyncedNames, fmt.Sprintf("%s/%s (%s)", p.detail.Namespace, p.detail.Name, p.detail.Kind))
				info.Details = append(info.Details, p.detail)
			}
			s.logger.Info("batch commit success", "count", len(pending))
		}
	}

	return info
}

type gvrWithScope struct {
	GVR        gvr.ResourceInfo
	Namespaced bool
	Kind       string
}

func (s *GenericSyncer) resolveResourceTypes(resourceTypes []string) []gvrWithScope {
	if len(resourceTypes) == 0 || (len(resourceTypes) == 1 && resourceTypes[0] == "All") {
		// Default: common resource types.
		resourceTypes = []string{"ConfigMap", "Secret", "Deployment", "StatefulSet", "DaemonSet", "CronJob", "Job", "Service", "Ingress"}
	}

	var results []gvrWithScope
	for _, kind := range resourceTypes {
		// Try common apiVersions for this kind.
		apiVersions := kindToAPIVersions(kind)
		for _, av := range apiVersions {
			gvrResult, namespaced, err := s.gvrResolver.Resolve(av, kind)
			if err == nil {
				results = append(results, gvrWithScope{
					GVR:        gvr.ResourceInfo{GVR: gvrResult, Namespaced: namespaced},
					Namespaced: namespaced,
					Kind:       kind,
				})
				break
			}
		}
	}
	return results
}

func kindToAPIVersions(kind string) []string {
	switch kind {
	case "ConfigMap", "Secret", "Service", "ServiceAccount", "PersistentVolumeClaim", "Namespace", "Pod":
		return []string{"v1"}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		return []string{"apps/v1"}
	case "CronJob", "Job":
		return []string{"batch/v1"}
	case "Ingress", "NetworkPolicy":
		return []string{"networking.k8s.io/v1"}
	case "Role", "RoleBinding":
		return []string{"rbac.authorization.k8s.io/v1"}
	case "ClusterRole", "ClusterRoleBinding":
		return []string{"rbac.authorization.k8s.io/v1"}
	case "HorizontalPodAutoscaler":
		return []string{"autoscaling/v2"}
	default:
		return []string{"v1", "apps/v1", "batch/v1"}
	}
}

func (s *GenericSyncer) matchesResourceTypes(kind string, resourceTypes []string) bool {
	if len(resourceTypes) == 0 {
		return true
	}
	for _, rt := range resourceTypes {
		if rt == "All" || rt == kind {
			return true
		}
	}
	return false
}

// shouldSkipResource returns true for K8s-managed resources that should not be
// synced to GitLab. Examples: the auto-created `default` ServiceAccount, the
// `kube-root-ca.crt` ConfigMap, controller-owned children (ReplicaSets owned
// by Deployments, Pods owned by anything), and Secrets of type
// `kubernetes.io/service-account-token` which are auto-created.
func shouldSkipResource(kind, namespace, name string, obj *unstructured.Unstructured) bool {
	// Resources owned by another controller — let the parent be the source of truth.
	if owners := obj.GetOwnerReferences(); len(owners) > 0 {
		return true
	}

	switch kind {
	case "ConfigMap":
		// Auto-injected per namespace by kube-controller-manager.
		if name == "kube-root-ca.crt" {
			return true
		}
	case "ServiceAccount":
		// Every namespace gets a `default` SA automatically.
		if name == "default" {
			return true
		}
	case "Secret":
		// Skip auto-mounted SA tokens / dockercfg.
		secretType, _, _ := unstructured.NestedString(obj.Object, "type")
		if secretType == "kubernetes.io/service-account-token" ||
			secretType == "kubernetes.io/dockercfg" ||
			secretType == "helm.sh/release.v1" {
			return true
		}
	case "Service":
		// The default `kubernetes` service in `default` namespace is cluster-managed.
		if namespace == "default" && name == "kubernetes" {
			return true
		}
	case "Pod", "ReplicaSet", "EndpointSlice", "Endpoints":
		// Pure runtime objects — never sync, even if no owner is set.
		return true
	case "Namespace":
		// System namespaces should not be reverse-synced.
		switch name {
		case "kube-system", "kube-public", "kube-node-lease", "default":
			return true
		}
	}

	return false
}
