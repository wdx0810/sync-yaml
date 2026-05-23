package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/configmap-sync/configmap-sync/internal/gitlab"
	"github.com/configmap-sync/configmap-sync/internal/parser"
	"github.com/configmap-sync/configmap-sync/internal/webhook"
)

// Scheduler manages sync mode scheduling (auto, scheduled, manual).
type Scheduler struct {
	engine       *engineImpl
	mode         string
	interval     time.Duration
	webhookRecv  webhook.Receiver
	mergeWindow  time.Duration
	pendingMu    sync.RWMutex
	pending      []gitlab.FileChange
	cancel       context.CancelFunc
	logger       *slog.Logger
}

// NewScheduler creates a new Scheduler for the given engine.
func NewScheduler(engine Engine, mode string, interval time.Duration, wr webhook.Receiver) *Scheduler {
	return &Scheduler{
		engine:      engine.(*engineImpl),
		mode:        mode,
		interval:    interval,
		webhookRecv: wr,
		mergeWindow: 2 * time.Second,
		logger:      slog.Default().With("component", "scheduler"),
	}
}

// Start begins the scheduling loop based on the configured mode.
func (s *Scheduler) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)

	switch s.mode {
	case "auto":
		go s.runAutoMode(ctx)
	case "scheduled":
		go s.runScheduledMode(ctx)
	case "manual":
		// Manual mode: no background scheduling, sync only on API trigger.
		s.logger.Info("manual sync mode active, waiting for API triggers")
	default:
		s.logger.Warn("unknown sync mode, defaulting to manual", "mode", s.mode)
	}

	return nil
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// GetPendingChanges returns pending changes in manual mode.
func (s *Scheduler) GetPendingChanges() []gitlab.FileChange {
	s.pendingMu.RLock()
	defer s.pendingMu.RUnlock()
	result := make([]gitlab.FileChange, len(s.pending))
	copy(result, s.pending)
	return result
}

// ClearPending clears the pending changes after a manual sync.
func (s *Scheduler) ClearPending() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.pending = nil
}

// runAutoMode listens for webhook events and triggers sync.
// It merges events received within a short window into a single sync.
func (s *Scheduler) runAutoMode(ctx context.Context) {
	s.logger.Info("starting auto sync mode")

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.webhookRecv.Events():
			// Collect the first event's files.
			fileSet := make(map[string]gitlab.FileChange)
			s.collectEventFiles(event, fileSet)

			// Wait for merge window to collect more events.
			timer := time.NewTimer(s.mergeWindow)
		mergeLoop:
			for {
				select {
				case ev := <-s.webhookRecv.Events():
					s.collectEventFiles(ev, fileSet)
				case <-timer.C:
					break mergeLoop
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}

			// Convert merged files to changes.
			changes := make([]gitlab.FileChange, 0, len(fileSet))
			for _, fc := range fileSet {
				changes = append(changes, fc)
			}

			s.logger.Info("auto sync triggered", "files", len(changes))
			_, err := s.engine.ForwardSync(ctx, ForwardSyncOptions{
				FileChanges: changes,
			})
			if err != nil {
				s.logger.Error("auto sync failed", "error", err)
			}
		}
	}
}

// collectEventFiles extracts YAML file changes from a webhook event.
func (s *Scheduler) collectEventFiles(event webhook.PushEvent, fileSet map[string]gitlab.FileChange) {
	for _, commit := range event.Commits {
		for _, path := range commit.Added {
			if isYAMLFile(path) {
				fileSet[path] = gitlab.FileChange{Path: path, ChangeType: gitlab.ChangeAdded}
			}
		}
		for _, path := range commit.Modified {
			if isYAMLFile(path) {
				fileSet[path] = gitlab.FileChange{Path: path, ChangeType: gitlab.ChangeModified}
			}
		}
		for _, path := range commit.Removed {
			if isYAMLFile(path) {
				fileSet[path] = gitlab.FileChange{Path: path, ChangeType: gitlab.ChangeDeleted}
			}
		}
	}
}

// runScheduledMode periodically checks GitLab for changes and syncs.
func (s *Scheduler) runScheduledMode(ctx context.Context) {
	s.logger.Info("starting scheduled sync mode", "interval", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := s.engine.CheckGitLabChanges(ctx)
			if err != nil {
				s.logger.Error("scheduled change check failed", "error", err)
				continue
			}

			if !result.HasChanges {
				s.logger.Debug("no changes detected, skipping sync")
				continue
			}

			s.logger.Info("scheduled sync triggered", "changes", len(result.Changes))
			_, err = s.engine.ForwardSync(ctx, ForwardSyncOptions{
				FileChanges: result.Changes,
			})
			if err != nil {
				s.logger.Error("scheduled sync failed", "error", err)
			}
		}
	}
}

// CheckAndMarkPending checks GitLab for changes and marks them as pending (manual mode).
func (s *Scheduler) CheckAndMarkPending(ctx context.Context) (*ChangeCheckResult, error) {
	result, err := s.engine.CheckGitLabChanges(ctx)
	if err != nil {
		return nil, err
	}

	if result.HasChanges {
		s.pendingMu.Lock()
		s.pending = result.Changes
		s.pendingMu.Unlock()

		// Mark all affected ConfigMaps as Pending using the parser.
		for _, change := range result.Changes {
			if change.Content != nil {
				cm, err := parser.Parse(change.Content)
				if err == nil {
					ns := cm.Metadata.Namespace
					if ns == "" {
						ns = s.engine.namespace
					}
					s.engine.updateStatus(ns, cm.Metadata.Name, "Pending")
				}
			}
		}
	}

	return result, nil
}

// isYAMLFile checks if a path has a YAML extension.
func isYAMLFile(path string) bool {
	for _, ext := range []string{".yaml", ".yml"} {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}
	return false
}
