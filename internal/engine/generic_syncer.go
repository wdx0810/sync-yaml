package engine

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

// NameFilter holds include/exclude regex patterns for resource name filtering.
type NameFilter struct {
	Include string // regex: only sync resources whose name matches (empty = all)
	Exclude string // regex: skip resources whose name matches
}

// matchesNameFilter returns true if the resource name passes the include/exclude filter.
func matchesNameFilter(name string, filter NameFilter) bool {
	// Include filter: if set, name must match.
	if filter.Include != "" {
		matched, err := regexp.MatchString(filter.Include, name)
		if err != nil || !matched {
			return false
		}
	}
	// Exclude filter: if set, name must NOT match.
	if filter.Exclude != "" {
		matched, err := regexp.MatchString(filter.Exclude, name)
		if err == nil && matched {
			return false
		}
	}
	return true
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
// Uses concurrent workers for K8s Get calls to handle large resource counts efficiently.
func (s *GenericSyncer) PreviewForward(ctx context.Context, gc gitlab.Client, dc k8sdynamic.Client, source *store.GitLabSource, target *store.K8sTarget, resourceTypes []string, filter NameFilter) ([]PendingChange, *SyncResultInfo, error) {
	info := &SyncResultInfo{}
	startTime := time.Now()
	s.logger.Info("preview started", "path", source.Path)

	files, err := gc.FetchFiles(ctx, source.Path)
	if err != nil {
		info.Errors = append(info.Errors, fmt.Sprintf("fetch files: %v", err))
		return nil, info, err
	}

	// Phase 1: Parse all resources from GitLab (CPU-bound, fast).
	type parsedResource struct {
		res *generic.Resource
		ns  string
	}
	var allResources []parsedResource

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
				continue
			}
			if !matchesNameFilter(res.Name, filter) {
				info.Skipped++
				continue
			}
			ns := res.Namespace
			if ns == "" && res.Namespaced {
				ns = target.Namespace
			}
			if !res.Namespaced {
				ns = ""
			}
			if res.Namespaced {
				res.Object.SetNamespace(ns)
			}
			res.Object.SetResourceVersion("")
			res.Object.SetManagedFields(nil)
			allResources = append(allResources, parsedResource{res: res, ns: ns})
		}
	}

	s.logger.Info("preview parsed", "total", info.Total, "toCompare", len(allResources),
		"parseTime", time.Since(startTime).String())

	// Phase 2: Batch List — build an index of existing K8s resources.
	// Key: "gvr|namespace|name" → *unstructured.Unstructured
	type listKey struct {
		gvr string
		ns  string
	}
	// Determine unique GVR+NS combinations we need to list.
	listTargets := make(map[listKey]bool)
	for _, pr := range allResources {
		key := listKey{gvr: pr.res.GVR.String(), ns: pr.ns}
		listTargets[key] = true
	}

	// Perform List calls (much fewer than individual Gets — typically 5-20 calls total).
	type indexKey struct {
		gvr  string
		ns   string
		name string
	}
	existingIndex := make(map[indexKey]*unstructured.Unstructured)

	listStart := time.Now()
	for lt := range listTargets {
		// Find GVR from the first matching resource.
		var gvr schema.GroupVersionResource
		for _, pr := range allResources {
			if pr.res.GVR.String() == lt.gvr {
				gvr = pr.res.GVR
				break
			}
		}
		if gvr.Resource == "" {
			continue
		}

		items, err := dc.List(ctx, lt.ns, gvr, "")
		if err != nil {
			s.logger.Warn("preview list failed", "gvr", lt.gvr, "ns", lt.ns, "error", err)
			continue
		}
		for _, obj := range items {
			key := indexKey{gvr: lt.gvr, ns: lt.ns, name: obj.GetName()}
			existingIndex[key] = obj
		}
	}
	s.logger.Info("preview listed", "listCalls", len(listTargets), "indexSize", len(existingIndex),
		"listTime", time.Since(listStart).String())

	// Phase 3: Compare in-memory (no more API calls).
	var pending []PendingChange
	for _, pr := range allResources {
		res := pr.res
		ns := pr.ns
		key := indexKey{gvr: res.GVR.String(), ns: ns, name: res.Name}

		existing, found := existingIndex[key]
		if found {
			if diff.IsSameContent(s.cleaner, existing, res.Object) {
				info.Skipped++
				continue
			}
		}

		action := "updated"
		var oldYAML string
		if !found {
			action = "created"
		}
		if existing != nil {
			cleanOld := s.cleaner.Clean(existing)
			if b, e := s.parser.Print(&generic.Resource{Object: cleanOld}); e == nil {
				oldYAML = string(b)
			}
		}
		cleanNew := s.cleaner.Clean(res.Object)
		newYAMLBytes, _ := s.parser.Print(&generic.Resource{Object: cleanNew})
		newYAML := string(newYAMLBytes)
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

	s.logger.Info("preview completed",
		"total", info.Total, "changes", len(pending), "skipped", info.Skipped,
		"duration", time.Since(startTime).String())
	return pending, info, nil
}

// ApplyChanges applies a pre-approved list of changes to K8s.
// Uses 10 concurrent workers for faster bulk apply.
func (s *GenericSyncer) ApplyChanges(ctx context.Context, dc k8sdynamic.Client, changes []PendingChange) *SyncResultInfo {
	info := &SyncResultInfo{Total: len(changes)}

	type applyResult struct {
		synced bool
		name   string
		detail history.ChangeDetail
		err    string
	}

	const applyWorkers = 10
	workers := applyWorkers
	if len(changes) < workers {
		workers = len(changes)
	}
	if workers == 0 {
		return info
	}

	jobs := make(chan int, len(changes))
	results := make(chan applyResult, len(changes))

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				ch := changes[idx]
				resources, err := s.parser.ParseMulti([]byte(ch.RawYAML))
				if err != nil || len(resources) == 0 {
					parseErr := "empty document"
					if err != nil {
						parseErr = err.Error()
					}
					s.logger.Error("apply parse failed", "ns", ch.Namespace, "kind", ch.Kind, "name", ch.Name, "error", parseErr)
					results <- applyResult{err: fmt.Sprintf("parse %s/%s/%s: %s", ch.Namespace, ch.Kind, ch.Name, parseErr), name: fmt.Sprintf("%s/%s (%s)", ch.Namespace, ch.Name, ch.Kind)}
					continue
				}
				res := resources[0]

				if res.GVR.Resource == "" {
					s.logger.Error("apply gvr unresolved", "ns", ch.Namespace, "kind", ch.Kind, "name", ch.Name)
					results <- applyResult{err: fmt.Sprintf("%s/%s/%s: 无法解析资源类型", ch.Namespace, ch.Kind, ch.Name), name: fmt.Sprintf("%s/%s (%s)", ch.Namespace, ch.Name, ch.Kind)}
					continue
				}

				ns := ch.Namespace
				if !res.Namespaced {
					ns = "" // cluster-scoped: empty namespace for API call
				}
				if res.Namespaced {
					res.Object.SetNamespace(ns)
				}
				res.Object.SetResourceVersion("")
				res.Object.SetManagedFields(nil)

				if err := dc.Apply(ctx, ns, res.GVR, res.Object); err != nil {
					s.logger.Error("apply failed", "ns", ns, "kind", ch.Kind, "name", ch.Name, "error", err)
					results <- applyResult{
						err:  fmt.Sprintf("%s/%s/%s: %v", ns, ch.Kind, ch.Name, err),
						name: fmt.Sprintf("%s/%s (%s)", ns, ch.Name, ch.Kind),
						detail: history.ChangeDetail{
							Name: ch.Name, Namespace: ns, Kind: ch.Kind, Action: "failed", Error: err.Error(),
						},
					}
					continue
				}

				results <- applyResult{
					synced: true,
					name:   fmt.Sprintf("%s/%s (%s)", ns, ch.Name, ch.Kind),
					detail: history.ChangeDetail{
						Name: ch.Name, Namespace: ns, Kind: ch.Kind, Action: ch.Action,
						OldYAML: ch.OldYAML, NewYAML: ch.NewYAML,
					},
				}
			}
		}()
	}

	for i := range changes {
		jobs <- i
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.synced {
			info.Synced++
			info.SyncedNames = append(info.SyncedNames, r.name)
			info.Details = append(info.Details, r.detail)
		} else {
			info.Failed++
			info.FailedNames = append(info.FailedNames, r.name)
			info.Errors = append(info.Errors, r.err)
			if r.detail.Name != "" {
				info.Details = append(info.Details, r.detail)
			}
		}
	}
	return info
}

// ForwardSync syncs resources from GitLab to K8s.
func (s *GenericSyncer) ForwardSync(ctx context.Context, gc gitlab.Client, dc k8sdynamic.Client, source *store.GitLabSource, target *store.K8sTarget, resourceTypes []string, filter NameFilter) *SyncResultInfo {
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

			// Filter by name (include/exclude regex).
			if !matchesNameFilter(res.Name, filter) {
				info.Skipped++
				continue
			}

			ns := res.Namespace
			if ns == "" && res.Namespaced {
				ns = target.Namespace
			}
			if !res.Namespaced {
				ns = "" // cluster-scoped resources must use empty namespace
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
func (s *GenericSyncer) ReverseSync(ctx context.Context, gc gitlab.Client, dc k8sdynamic.Client, source *store.GitLabSource, target *store.K8sTarget, resourceTypes []string, taskName string, filter NameFilter) *SyncResultInfo {
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

				// User-defined name filter (include/exclude regex).
				if !matchesNameFilter(name, filter) {
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

		commitMsg := fmt.Sprintf("[%s] Sync from K8s: %d resource(s)", taskName, len(pending))
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
		// Skip well-known platform-managed secrets (e.g. Huawei CCE ELB certs).
		if name == "default-secret" || name == "paas-elb" || name == "paas.elb" ||
			strings.HasPrefix(name, "default-token-") ||
			strings.HasPrefix(name, "sh.helm.release") {
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
	case "ClusterRole", "ClusterRoleBinding":
		// Skip K8s built-in system roles (system:*, kubeadm:*, etc.)
		if strings.HasPrefix(name, "system:") ||
			strings.HasPrefix(name, "kubeadm:") ||
			strings.HasPrefix(name, "calico") ||
			strings.HasPrefix(name, "flannel") ||
			strings.HasPrefix(name, "csi-") ||
			strings.HasPrefix(name, "everest-") ||
			strings.HasPrefix(name, "kube-") {
			return true
		}
	}

	return false
}
