package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/configmap-sync/configmap-sync/internal/gitlab"
	"github.com/configmap-sync/configmap-sync/internal/history"
	"github.com/configmap-sync/configmap-sync/internal/k8s"
	k8sdynamic "github.com/configmap-sync/configmap-sync/internal/k8s/dynamic"
	gvrpkg "github.com/configmap-sync/configmap-sync/internal/k8s/gvr"
	"github.com/configmap-sync/configmap-sync/internal/store"

	corev1 "k8s.io/api/core/v1"
)

type TaskManager interface {
	StartTask(task *store.SyncTask) error
	StopTask(taskID string) error
	TriggerSync(taskID string) (*SyncResultInfo, error)
	PreviewSync(taskID string) (*PreviewResult, error)
	ApplyChanges(taskID string, changes []PendingChange) (*SyncResultInfo, error)
	GetTaskStatus(taskID string) *TaskRuntimeStatus
	StopAll()
	RestoreRunningTasks(tasks []store.SyncTask) error
}

// PreviewResult is returned from PreviewSync for user review.
type PreviewResult struct {
	Direction string          `json:"direction"`
	Changes   []PendingChange `json:"changes"`
	Summary   *SyncResultInfo `json:"summary"`
}

type SyncResultInfo struct {
	Total        int                    `json:"total"`
	Synced       int                    `json:"synced"`
	Failed       int                    `json:"failed"`
	Skipped      int                    `json:"skipped"`
	SyncedNames  []string               `json:"syncedNames,omitempty"`
	SkippedNames []string               `json:"skippedNames,omitempty"`
	FailedNames  []string               `json:"failedNames,omitempty"`
	Errors       []string               `json:"errors,omitempty"`
	Details      []history.ChangeDetail `json:"details,omitempty"`
}

type TaskRuntimeStatus struct {
	TaskID       string
	Status       string
	LastSyncTime time.Time
	LastResult   string
	ErrorMessage string
}

type runningTask struct {
	cancel context.CancelFunc
	status *TaskRuntimeStatus
}

type taskManager struct {
	sourceStore  store.SourceStore
	targetStore  store.TargetStore
	taskStore    store.TaskStore
	historyStore history.Store
	running      map[string]*runningTask
	mu           sync.RWMutex
	logger       *slog.Logger
}

func NewTaskManager(ss store.SourceStore, ts store.TargetStore, tks store.TaskStore, hs history.Store) TaskManager {
	return &taskManager{
		sourceStore: ss, targetStore: ts, taskStore: tks, historyStore: hs,
		running: make(map[string]*runningTask),
		logger:  slog.Default().With("component", "task-manager"),
	}
}

func (m *taskManager) resolveClients(task *store.SyncTask) (gitlab.Client, k8s.Client, *store.GitLabSource, *store.K8sTarget, error) {
	var source *store.GitLabSource
	var target *store.K8sTarget
	var err error

	if task.Direction == "reverse" {
		target, err = m.targetStore.Get(task.SourceName)
		if err != nil { return nil, nil, nil, nil, fmt.Errorf("k8s %q not found: %w", task.SourceName, err) }
		source, err = m.sourceStore.Get(task.TargetName)
		if err != nil { return nil, nil, nil, nil, fmt.Errorf("gitlab %q not found: %w", task.TargetName, err) }
	} else {
		source, err = m.sourceStore.Get(task.SourceName)
		if err != nil { return nil, nil, nil, nil, fmt.Errorf("gitlab %q not found: %w", task.SourceName, err) }
		target, err = m.targetStore.Get(task.TargetName)
		if err != nil { return nil, nil, nil, nil, fmt.Errorf("k8s %q not found: %w", task.TargetName, err) }
	}

	gc, err := gitlab.NewClient(source.URL, source.Token, source.ProjectID, source.Branch, source.Path)
	if err != nil { return nil, nil, nil, nil, err }

	var kc k8s.Client
	if target.KubeconfigContent != "" {
		kc, err = k8s.NewClientFromContent(target.KubeconfigContent)
	} else {
		kc, err = k8s.NewClient("")
	}
	if err != nil { return nil, nil, nil, nil, err }

	return gc, kc, source, target, nil
}

func (m *taskManager) StartTask(task *store.SyncTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.running[task.ID]; exists {
		return fmt.Errorf("task %s already running", task.ID)
	}
	gc, kc, source, target, err := m.resolveClients(task)
	if err != nil { return err }

	ctx, cancel := context.WithCancel(context.Background())
	rt := &runningTask{cancel: cancel, status: &TaskRuntimeStatus{TaskID: task.ID, Status: "running"}}
	m.running[task.ID] = rt
	task.Status = "running"
	_ = m.taskStore.Update(task.ID, task)
	go m.runTask(ctx, task, gc, kc, source, target, rt)
	m.logger.Info("task started", "id", task.ID, "name", task.Name)
	return nil
}

func (m *taskManager) StopTask(taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, exists := m.running[taskID]
	if !exists { return nil }
	rt.cancel()
	delete(m.running, taskID)
	if task, err := m.taskStore.Get(taskID); err == nil {
		task.Status = "paused"
		_ = m.taskStore.Update(taskID, task)
	}
	return nil
}

func (m *taskManager) TriggerSync(taskID string) (*SyncResultInfo, error) {
	m.mu.RLock()
	rt, exists := m.running[taskID]
	m.mu.RUnlock()

	task, err := m.taskStore.Get(taskID)
	if err != nil { return nil, err }

	gc, kc, source, target, err := m.resolveClients(task)
	if err != nil { return nil, err }

	info := m.doSync(context.Background(), task, gc, kc, source, target)

	if exists && rt != nil {
		rt.status.LastSyncTime = time.Now()
		if info.Failed > 0 { rt.status.LastResult = "failed" } else { rt.status.LastResult = "success" }
	}
	return info, nil
}

func (m *taskManager) GetTaskStatus(taskID string) *TaskRuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if rt, ok := m.running[taskID]; ok {
		return rt.status
	}
	return nil
}

// PreviewSync computes the changes a forward sync would apply, without modifying K8s.
func (m *taskManager) PreviewSync(taskID string) (*PreviewResult, error) {
	task, err := m.taskStore.Get(taskID)
	if err != nil {
		return nil, err
	}

	dir := task.Direction
	if dir == "" {
		dir = "forward"
	}
	if dir != "forward" {
		return nil, fmt.Errorf("预览仅支持正向同步 (GitLab → K8s)")
	}

	gc, _, source, target, err := m.resolveClients(task)
	if err != nil {
		return nil, err
	}

	effectiveSource := *source
	effectiveTarget := *target
	if task.SourcePath != "" {
		effectiveSource.Path = task.SourcePath
	}
	if effectiveSource.Path == "" {
		effectiveSource.Path = "/"
	}
	if task.TargetNS != "" {
		effectiveTarget.Namespace = task.TargetNS
	}
	if effectiveTarget.Namespace == "" {
		effectiveTarget.Namespace = "default"
	}

	dynClient, err := k8s.ParseKubeconfigForDynamic(effectiveTarget.KubeconfigContent)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	// Build a discovery-backed resolver so CRDs/HPA/etc. resolve to a real GVR.
	disc, _ := k8s.ParseKubeconfigForDiscovery(effectiveTarget.KubeconfigContent)
	dc := k8sdynamic.NewClient(dynClient)
	syncer := NewGenericSyncer(gvrpkg.NewResolver(disc))

	resourceTypes := task.ResourceTypes
	if len(resourceTypes) == 0 {
		resourceTypes = []string{"All"}
	}

	ctx := context.Background()
	changes, summary, err := syncer.PreviewForward(ctx, gc, dc, &effectiveSource, &effectiveTarget, resourceTypes)
	if err != nil {
		return nil, err
	}

	return &PreviewResult{
		Direction: dir,
		Changes:   changes,
		Summary:   summary,
	}, nil
}

// ApplyChanges applies a user-approved list of changes for the given task.
func (m *taskManager) ApplyChanges(taskID string, changes []PendingChange) (*SyncResultInfo, error) {
	task, err := m.taskStore.Get(taskID)
	if err != nil {
		return nil, err
	}

	_, _, _, target, err := m.resolveClients(task)
	if err != nil {
		return nil, err
	}

	effectiveTarget := *target
	if task.TargetNS != "" {
		effectiveTarget.Namespace = task.TargetNS
	}
	if effectiveTarget.Namespace == "" {
		effectiveTarget.Namespace = "default"
	}

	dynClient, err := k8s.ParseKubeconfigForDynamic(effectiveTarget.KubeconfigContent)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	disc, _ := k8s.ParseKubeconfigForDiscovery(effectiveTarget.KubeconfigContent)
	dc := k8sdynamic.NewClient(dynClient)
	syncer := NewGenericSyncer(gvrpkg.NewResolver(disc))

	// Re-hydrate RawYAML from NewYAML (clients send NewYAML, not RawYAML).
	for i := range changes {
		if changes[i].RawYAML == "" {
			changes[i].RawYAML = changes[i].NewYAML
		}
	}

	info := syncer.ApplyChanges(context.Background(), dc, changes)
	m.finishTask(task, "forward", effectiveTarget.Namespace, info)
	return info, nil
}

func (m *taskManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rt := range m.running {
		rt.cancel()
		if task, err := m.taskStore.Get(id); err == nil {
			task.Status = "paused"
			_ = m.taskStore.Update(id, task)
		}
	}
	m.running = make(map[string]*runningTask)
}

func (m *taskManager) RestoreRunningTasks(tasks []store.SyncTask) error {
	for i := range tasks {
		if tasks[i].Status == "running" {
			if err := m.StartTask(&tasks[i]); err != nil {
				tasks[i].Status = "error"
				tasks[i].ErrorMessage = err.Error()
				_ = m.taskStore.Update(tasks[i].ID, &tasks[i])
			}
		}
	}
	return nil
}

func (m *taskManager) runTask(ctx context.Context, task *store.SyncTask, gc gitlab.Client, kc k8s.Client, source *store.GitLabSource, target *store.K8sTarget, rt *runningTask) {
	defer func() { m.mu.Lock(); delete(m.running, task.ID); m.mu.Unlock() }()
	switch task.SyncMode {
	case "scheduled":
		m.runScheduled(ctx, task, gc, kc, source, target, rt)
	case "manual":
		<-ctx.Done()
	case "auto":
		if task.Direction == "reverse" {
			m.runWatchMode(ctx, task, gc, kc, source, target, rt)
		} else {
			// For forward auto mode, fall back to scheduled with short interval.
			m.runScheduled(ctx, task, gc, kc, source, target, rt)
		}
	}
}

func (m *taskManager) runScheduled(ctx context.Context, task *store.SyncTask, gc gitlab.Client, kc k8s.Client, source *store.GitLabSource, target *store.K8sTarget, rt *runningTask) {
	interval := time.Duration(task.Interval) * time.Second
	if interval < 30*time.Second { interval = 300 * time.Second }
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	info := m.doSync(ctx, task, gc, kc, source, target)
	rt.status.LastSyncTime = time.Now()
	if info.Failed > 0 { rt.status.LastResult = "failed" } else { rt.status.LastResult = "success" }

	for {
		select {
		case <-ctx.Done(): return
		case <-ticker.C:
			info := m.doSync(ctx, task, gc, kc, source, target)
			rt.status.LastSyncTime = time.Now()
			if info.Failed > 0 { rt.status.LastResult = "failed" } else { rt.status.LastResult = "success" }
		}
	}
}

// runWatchMode uses K8s Watch API to detect ConfigMap changes and sync to GitLab immediately.
func (m *taskManager) runWatchMode(ctx context.Context, task *store.SyncTask, gc gitlab.Client, kc k8s.Client, source *store.GitLabSource, target *store.K8sTarget, rt *runningTask) {
	// Use task-level namespace if specified, otherwise fall back to target namespace.
	ns := target.Namespace
	if task.TargetNS != "" {
		ns = task.TargetNS
	}
	namespaces := strings.Split(ns, ",")
	for i := range namespaces { namespaces[i] = strings.TrimSpace(namespaces[i]) }

	m.logger.Info("starting watch mode", "task", task.Name, "namespaces", namespaces)

	for _, ns := range namespaces {
		if ns == "" { continue }
		ch, err := kc.WatchConfigMaps(ctx, ns)
		if err != nil {
			m.logger.Error("failed to watch namespace", "namespace", ns, "error", err)
			continue
		}
		go func(namespace string, events <-chan *corev1.ConfigMap) {
			for {
				select {
				case <-ctx.Done():
					return
				case cm, ok := <-events:
					if !ok { return }
					m.logger.Info("watch: configmap changed", "namespace", namespace, "name", cm.Name)
					// Trigger a full reverse sync for this task.
					info := m.doSync(ctx, task, gc, kc, source, target)
					rt.status.LastSyncTime = time.Now()
					if info.Failed > 0 { rt.status.LastResult = "failed" } else { rt.status.LastResult = "success" }
				}
			}
		}(ns, ch)
	}

	// Block until context is cancelled.
	<-ctx.Done()
}

// doSync always uses the generic syncer (dynamic client + Server-Side Apply) for all resource types.
func (m *taskManager) doSync(ctx context.Context, task *store.SyncTask, gc gitlab.Client, _ k8s.Client, source *store.GitLabSource, target *store.K8sTarget) *SyncResultInfo {
	// Use task-level path and namespace (required fields).
	// Fall back to source/target defaults only for backward compatibility with old tasks.
	effectiveSource := *source
	effectiveTarget := *target
	if task.SourcePath != "" {
		effectiveSource.Path = task.SourcePath
	}
	if effectiveSource.Path == "" {
		effectiveSource.Path = "/"
	}
	if task.TargetNS != "" {
		effectiveTarget.Namespace = task.TargetNS
	}
	if effectiveTarget.Namespace == "" {
		effectiveTarget.Namespace = "default"
	}

	// Create dynamic client from kubeconfig.
	dynClient, err := k8s.ParseKubeconfigForDynamic(effectiveTarget.KubeconfigContent)
	if err != nil {
		m.setTaskError(task, fmt.Sprintf("create dynamic client: %v", err))
		return &SyncResultInfo{Errors: []string{err.Error()}}
	}

	disc, _ := k8s.ParseKubeconfigForDiscovery(effectiveTarget.KubeconfigContent)
	dc := k8sdynamic.NewClient(dynClient)
	syncer := NewGenericSyncer(gvrpkg.NewResolver(disc))

	// Default resource types if none specified.
	resourceTypes := task.ResourceTypes
	if len(resourceTypes) == 0 {
		resourceTypes = []string{"All"}
	}

	var info *SyncResultInfo
	dir := task.Direction
	if dir == "" { dir = "forward" }

	switch dir {
	case "forward":
		info = syncer.ForwardSync(ctx, gc, dc, &effectiveSource, &effectiveTarget, resourceTypes)
	case "reverse":
		info = syncer.ReverseSync(ctx, gc, dc, &effectiveSource, &effectiveTarget, resourceTypes)
	default:
		m.setTaskError(task, "unknown direction: "+dir)
		return &SyncResultInfo{Errors: []string{"unknown direction"}}
	}

	m.finishTask(task, dir, effectiveTarget.Namespace, info)
	return info
}

func (m *taskManager) finishTask(task *store.SyncTask, direction, namespace string, info *SyncResultInfo) {
	task.LastSyncTime = time.Now().Format(time.RFC3339)
	if info.Failed > 0 {
		task.LastSyncResult = fmt.Sprintf("部分成功: %d/%d 成功, %d 失败", info.Synced, info.Total, info.Failed)
	} else {
		task.LastSyncResult = fmt.Sprintf("成功: %d/%d 已同步", info.Synced, info.Total)
	}
	// Preserve current Status. Whether the task is currently running
	// (scheduled/auto loop) or paused (manual trigger on a stopped task) is
	// authoritative state — do NOT clobber it here. Re-read from the store so
	// we don't accidentally overwrite a Pause that happened mid-sync.
	if cur, err := m.taskStore.Get(task.ID); err == nil {
		task.Status = cur.Status
	}
	task.ErrorMessage = ""
	_ = m.taskStore.Update(task.ID, task)

	if m.historyStore != nil {
		_ = m.historyStore.Save(&history.SyncRecord{
			ID:            fmt.Sprintf("%d", time.Now().UnixNano()),
			Timestamp:     time.Now(),
			TaskName:      task.Name,
			ConfigMapName: fmt.Sprintf("%d/%d synced", info.Synced, info.Total),
			Namespace:     namespace,
			Direction:     direction,
			ChangeType:    "sync",
			Status:        "Synced",
			Details:       info.Details,
		})
	}
	m.logger.Info("sync done", "task", task.Name, "direction", direction, "synced", info.Synced, "total", info.Total, "failed", info.Failed)
}

func (m *taskManager) setTaskError(task *store.SyncTask, errMsg string) {
	task.Status = "error"
	task.ErrorMessage = errMsg
	task.LastSyncTime = time.Now().Format(time.RFC3339)
	task.LastSyncResult = "失败: " + errMsg
	_ = m.taskStore.Update(task.ID, task)
}

