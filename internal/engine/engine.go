package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/configmap-sync/configmap-sync/internal/gitlab"
	"github.com/configmap-sync/configmap-sync/internal/history"
	"github.com/configmap-sync/configmap-sync/internal/k8s"
	"github.com/configmap-sync/configmap-sync/internal/parser"
)

// SyncResult represents the result of a sync operation.
type SyncResult struct {
	Synced  []string `json:"synced"`
	Skipped []string `json:"skipped"`
	Failed  []string `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// ChangeCheckResult represents the result of a GitLab change check.
type ChangeCheckResult struct {
	HasChanges bool                `json:"hasChanges"`
	Changes    []gitlab.FileChange `json:"changes"`
}

// ConfigMapStatus represents the sync status of a managed ConfigMap.
type ConfigMapStatus struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	SyncStatus   string    `json:"syncStatus"` // "Synced", "Pending", "Failed", "Drifted"
	LastSyncTime time.Time `json:"lastSyncTime"`
}

// ConfigMapDetail represents detailed info about a ConfigMap including diff.
type ConfigMapDetail struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	DesiredState map[string]string `json:"desiredState"`
	ActualState  map[string]string `json:"actualState"`
	Diff         []DiffEntry       `json:"diff"`
	SyncStatus   string            `json:"syncStatus"`
	LastSyncTime time.Time         `json:"lastSyncTime"`
}

// DiffEntry represents a single field difference.
type DiffEntry struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// ForwardSyncOptions configures a forward sync operation.
type ForwardSyncOptions struct {
	FileChanges []gitlab.FileChange
	FullSync    bool
}

// Engine defines the interface for the sync engine.
type Engine interface {
	ForwardSync(ctx context.Context, opts ForwardSyncOptions) (*SyncResult, error)
	ForwardSyncOne(ctx context.Context, namespace, name string) (*SyncResult, error)
	ReverseSync(ctx context.Context, namespace, name string) (*SyncResult, error)
	CheckGitLabChanges(ctx context.Context) (*ChangeCheckResult, error)
	GetManagedConfigMaps(ctx context.Context) ([]ConfigMapStatus, error)
	GetConfigMapDetail(ctx context.Context, namespace, name string) (*ConfigMapDetail, error)
}

// engineImpl is the concrete implementation of Engine.
type engineImpl struct {
	gitlabClient gitlab.Client
	k8sClient    k8s.Client
	historyStore history.Store
	namespace    string
	basePath     string
	lastCommit   string

	// statusCache tracks sync status per ConfigMap (key: "namespace/name").
	statusCache map[string]*ConfigMapStatus
	mu          sync.RWMutex
	logger      *slog.Logger
}

// NewEngine creates a new Sync Engine.
func NewEngine(gc gitlab.Client, kc k8s.Client, hs history.Store, namespace, basePath string) Engine {
	return &engineImpl{
		gitlabClient: gc,
		k8sClient:    kc,
		historyStore: hs,
		namespace:    namespace,
		basePath:     basePath,
		statusCache:  make(map[string]*ConfigMapStatus),
		logger:       slog.Default().With("component", "engine"),
	}
}

// ForwardSync executes a forward sync (GitLab → K8s).
func (e *engineImpl) ForwardSync(ctx context.Context, opts ForwardSyncOptions) (*SyncResult, error) {
	result := &SyncResult{}

	var files []gitlab.FileContent
	var err error

	if opts.FullSync {
		// Full sync: fetch all files from GitLab.
		files, err = e.gitlabClient.FetchFiles(ctx, e.basePath)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch files from gitlab: %w", err)
		}
	} else if len(opts.FileChanges) > 0 {
		// Incremental sync: use provided file changes.
		for _, fc := range opts.FileChanges {
			if fc.ChangeType == gitlab.ChangeDeleted {
				// Parse the path to get ConfigMap name for deletion.
				// For now, skip deletions in forward sync.
				result.Skipped = append(result.Skipped, fc.Path)
				continue
			}
			if fc.Content != nil {
				files = append(files, gitlab.FileContent{
					Path:    fc.Path,
					Content: fc.Content,
				})
			}
		}
	} else {
		return result, nil
	}

	for _, f := range files {
		if err := e.syncFile(ctx, f, result); err != nil {
			e.logger.Error("failed to sync file", "path", f.Path, "error", err)
		}
	}

	return result, nil
}

// ForwardSyncOne syncs a single ConfigMap by namespace and name.
func (e *engineImpl) ForwardSyncOne(ctx context.Context, namespace, name string) (*SyncResult, error) {
	result := &SyncResult{}

	// Fetch all files and find the matching one.
	files, err := e.gitlabClient.FetchFiles(ctx, e.basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch files from gitlab: %w", err)
	}

	for _, f := range files {
		cm, err := parser.Parse(f.Content)
		if err != nil {
			continue
		}
		ns := cm.Metadata.Namespace
		if ns == "" {
			ns = e.namespace
		}
		if ns == namespace && cm.Metadata.Name == name {
			if err := e.syncFile(ctx, f, result); err != nil {
				e.logger.Error("failed to sync file", "path", f.Path, "error", err)
			}
			return result, nil
		}
	}

	return nil, fmt.Errorf("configmap %s/%s not found in gitlab", namespace, name)
}

// ReverseSync writes a K8s ConfigMap back to GitLab.
func (e *engineImpl) ReverseSync(ctx context.Context, namespace, name string) (*SyncResult, error) {
	result := &SyncResult{}

	// Get actual state from K8s.
	actual, err := e.k8sClient.GetConfigMap(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get configmap from k8s: %w", err)
	}

	// Convert to parser format and print as YAML.
	cm := &parser.ConfigMapData{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata: parser.Metadata{
			Name:      actual.Name,
			Namespace: actual.Namespace,
		},
		Data: actual.Data,
	}

	yamlContent, err := parser.Print(cm)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal configmap to yaml: %w", err)
	}

	// Find the file path in GitLab for this ConfigMap.
	filePath := fmt.Sprintf("%s/%s.yaml", namespace, name)
	message := fmt.Sprintf("Reverse sync: update %s/%s from K8s cluster", namespace, name)

	if err := e.gitlabClient.CommitFile(ctx, filePath, yamlContent, message); err != nil {
		result.Failed = append(result.Failed, name)
		result.Errors = append(result.Errors, err.Error())

		e.saveRecord(namespace, name, "reverse", "modified", "Failed", "", "", err.Error())
		return result, fmt.Errorf("failed to commit to gitlab: %w", err)
	}

	result.Synced = append(result.Synced, name)
	e.updateStatus(namespace, name, "Synced")
	e.saveRecord(namespace, name, "reverse", "modified", "Synced", "", "", "")

	return result, nil
}

// CheckGitLabChanges checks for changes in GitLab since the last sync.
func (e *engineImpl) CheckGitLabChanges(ctx context.Context) (*ChangeCheckResult, error) {
	if e.lastCommit == "" {
		// No previous commit tracked; do a full check.
		files, err := e.gitlabClient.FetchFiles(ctx, e.basePath)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch files: %w", err)
		}
		changes := make([]gitlab.FileChange, len(files))
		for i, f := range files {
			changes[i] = gitlab.FileChange{
				Path:       f.Path,
				ChangeType: gitlab.ChangeModified,
				Content:    f.Content,
			}
		}
		return &ChangeCheckResult{
			HasChanges: len(changes) > 0,
			Changes:    changes,
		}, nil
	}

	changes, err := e.gitlabClient.CheckChanges(ctx, e.lastCommit)
	if err != nil {
		return nil, fmt.Errorf("failed to check changes: %w", err)
	}

	return &ChangeCheckResult{
		HasChanges: len(changes) > 0,
		Changes:    changes,
	}, nil
}

// GetManagedConfigMaps returns the status of all managed ConfigMaps.
func (e *engineImpl) GetManagedConfigMaps(ctx context.Context) ([]ConfigMapStatus, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	statuses := make([]ConfigMapStatus, 0, len(e.statusCache))
	for _, s := range e.statusCache {
		statuses = append(statuses, *s)
	}
	return statuses, nil
}

// GetConfigMapDetail returns detailed info about a specific ConfigMap.
func (e *engineImpl) GetConfigMapDetail(ctx context.Context, namespace, name string) (*ConfigMapDetail, error) {
	// Get desired state from GitLab.
	files, err := e.gitlabClient.FetchFiles(ctx, e.basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch files from gitlab: %w", err)
	}

	var desired map[string]string
	for _, f := range files {
		cm, err := parser.Parse(f.Content)
		if err != nil {
			continue
		}
		ns := cm.Metadata.Namespace
		if ns == "" {
			ns = e.namespace
		}
		if ns == namespace && cm.Metadata.Name == name {
			desired = cm.Data
			break
		}
	}

	if desired == nil {
		return nil, fmt.Errorf("configmap %s/%s not found in gitlab", namespace, name)
	}

	// Get actual state from K8s.
	actual, err := e.k8sClient.GetConfigMap(ctx, namespace, name)
	var actualData map[string]string
	if err == nil {
		actualData = actual.Data
	}

	// Compute diff.
	diff := computeDiff(desired, actualData)

	e.mu.RLock()
	key := fmt.Sprintf("%s/%s", namespace, name)
	status := "Synced"
	var lastSync time.Time
	if s, ok := e.statusCache[key]; ok {
		status = s.SyncStatus
		lastSync = s.LastSyncTime
	}
	e.mu.RUnlock()

	return &ConfigMapDetail{
		Name:         name,
		Namespace:    namespace,
		DesiredState: desired,
		ActualState:  actualData,
		Diff:         diff,
		SyncStatus:   status,
		LastSyncTime: lastSync,
	}, nil
}

// syncFile parses a YAML file and applies it to K8s.
func (e *engineImpl) syncFile(ctx context.Context, f gitlab.FileContent, result *SyncResult) error {
	cm, err := parser.Parse(f.Content)
	if err != nil {
		e.logger.Warn("skipping invalid yaml file", "path", f.Path, "error", err)
		result.Skipped = append(result.Skipped, f.Path)
		return nil
	}

	namespace := cm.Metadata.Namespace
	if namespace == "" {
		namespace = e.namespace
	}

	k8sCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cm.Metadata.Name,
			Namespace:   namespace,
			Labels:      cm.Metadata.Labels,
			Annotations: cm.Metadata.Annotations,
		},
		Data: cm.Data,
	}

	if err := e.k8sClient.ApplyConfigMap(ctx, namespace, k8sCM); err != nil {
		result.Failed = append(result.Failed, cm.Metadata.Name)
		result.Errors = append(result.Errors, err.Error())
		e.updateStatus(namespace, cm.Metadata.Name, "Failed")
		e.saveRecord(namespace, cm.Metadata.Name, "forward", "modified", "Failed", "", "", err.Error())
		return err
	}

	result.Synced = append(result.Synced, cm.Metadata.Name)
	e.updateStatus(namespace, cm.Metadata.Name, "Synced")
	e.saveRecord(namespace, cm.Metadata.Name, "forward", "modified", "Synced", "", "", "")
	return nil
}

// updateStatus updates the status cache for a ConfigMap.
func (e *engineImpl) updateStatus(namespace, name, status string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	e.statusCache[key] = &ConfigMapStatus{
		Name:         name,
		Namespace:    namespace,
		SyncStatus:   status,
		LastSyncTime: time.Now(),
	}
}

// saveRecord saves a sync record to the history store.
func (e *engineImpl) saveRecord(namespace, name, direction, changeType, status, before, after, errMsg string) {
	record := &history.SyncRecord{
		ID:            fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp:     time.Now(),
		ConfigMapName: name,
		Namespace:     namespace,
		Direction:     direction,
		ChangeType:    changeType,
		Status:        status,
		BeforeSummary: before,
		AfterSummary:  after,
		ErrorMessage:  errMsg,
	}
	if err := e.historyStore.Save(record); err != nil {
		e.logger.Error("failed to save sync record", "error", err)
	}
}

// computeDiff compares desired and actual data maps and returns differences.
func computeDiff(desired, actual map[string]string) []DiffEntry {
	var diffs []DiffEntry

	// Check for keys in desired that differ or are missing in actual.
	for k, dv := range desired {
		av, ok := actual[k]
		if !ok {
			diffs = append(diffs, DiffEntry{Field: k, Expected: dv, Actual: ""})
		} else if dv != av {
			diffs = append(diffs, DiffEntry{Field: k, Expected: dv, Actual: av})
		}
	}

	// Check for keys in actual that are not in desired.
	for k, av := range actual {
		if _, ok := desired[k]; !ok {
			diffs = append(diffs, DiffEntry{Field: k, Expected: "", Actual: av})
		}
	}

	return diffs
}

// SetLastCommit sets the last known commit SHA for change detection.
func (e *engineImpl) SetLastCommit(sha string) {
	e.lastCommit = sha
}
