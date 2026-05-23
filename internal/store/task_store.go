package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TaskStore manages sync task persistence.
type TaskStore interface {
	List() ([]SyncTask, error)
	Get(id string) (*SyncTask, error)
	Create(task *SyncTask) error
	Update(id string, task *SyncTask) error
	Delete(id string) error
}

type taskStore struct {
	tasks       []SyncTask
	storagePath string
	sourceStore SourceStore
	targetStore TargetStore
	mu          sync.RWMutex
	logger      *slog.Logger
}

func NewTaskStore(storagePath string, ss SourceStore, ts TargetStore) TaskStore {
	s := &taskStore{
		storagePath: storagePath,
		sourceStore: ss,
		targetStore: ts,
		logger:      slog.Default().With("component", "task-store"),
	}
	s.load()
	return s
}

func (s *taskStore) filePath() string {
	return filepath.Join(s.storagePath, "sync_tasks.json")
}

func (s *taskStore) load() {
	data, err := os.ReadFile(s.filePath())
	if err != nil {
		return
	}
	var tasks []SyncTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		s.logger.Warn("failed to parse sync_tasks.json, starting empty", "error", err)
		return
	}
	s.tasks = tasks
}

func (s *taskStore) save() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(), data, 0644)
}

func (s *taskStore) List() ([]SyncTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SyncTask, len(s.tasks))
	copy(result, s.tasks)
	return result, nil
}

func (s *taskStore) Get(id string) (*SyncTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tasks {
		if t.ID == id {
			out := t
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "task", Name: id}
}

func (s *taskStore) Create(task *SyncTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate references based on direction.
	// Forward: sourceName = GitLab, targetName = K8s
	// Reverse: sourceName = K8s, targetName = GitLab
	if task.Direction == "reverse" {
		// sourceName is a K8s target name.
		if s.targetStore != nil {
			if _, err := s.targetStore.Get(task.SourceName); err != nil {
				return fmt.Errorf("K8s 集群 %q not found", task.SourceName)
			}
		}
		// targetName is a GitLab source name.
		if s.sourceStore != nil {
			if _, err := s.sourceStore.Get(task.TargetName); err != nil {
				return fmt.Errorf("GitLab 源 %q not found", task.TargetName)
			}
		}
	} else {
		// Forward: sourceName is GitLab, targetName is K8s.
		if s.sourceStore != nil {
			if _, err := s.sourceStore.Get(task.SourceName); err != nil {
				return fmt.Errorf("GitLab 源 %q not found", task.SourceName)
			}
		}
		if s.targetStore != nil {
			if _, err := s.targetStore.Get(task.TargetName); err != nil {
				return fmt.Errorf("K8s 集群 %q not found", task.TargetName)
			}
		}
	}

	// Generate ID and set defaults.
	task.ID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	task.Status = "paused"
	task.LastSyncTime = ""
	task.LastSyncResult = ""
	if task.Direction == "" {
		task.Direction = "forward"
	}

	s.tasks = append(s.tasks, *task)
	return s.save()
}

func (s *taskStore) Update(id string, task *SyncTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.tasks {
		if existing.ID == id {
			task.ID = id
			s.tasks[i] = *task
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "task", Name: id}
}

func (s *taskStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.tasks {
		if existing.ID == id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "task", Name: id}
}
