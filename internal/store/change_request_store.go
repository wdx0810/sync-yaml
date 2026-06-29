package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ChangeRequestStore manages ConfigMap change requests.
// It is a standalone JSON-backed store and does not interact with the sync engine.
type ChangeRequestStore interface {
	List(status string) ([]ChangeRequest, error)
	Get(id string) (*ChangeRequest, error)
	Create(cr *ChangeRequest) error
	Update(cr *ChangeRequest) error
	Delete(id string) error
}

type changeRequestStore struct {
	requests    []ChangeRequest
	storagePath string
	mu          sync.RWMutex
	logger      *slog.Logger
}

func NewChangeRequestStore(storagePath string) ChangeRequestStore {
	s := &changeRequestStore{
		storagePath: storagePath,
		logger:      slog.Default().With("component", "change-request-store"),
	}
	s.load()
	return s
}

func (s *changeRequestStore) filePath() string {
	return filepath.Join(s.storagePath, "change_requests.json")
}

func (s *changeRequestStore) load() {
	data, err := os.ReadFile(s.filePath())
	if err != nil {
		return
	}
	var reqs []ChangeRequest
	if err := json.Unmarshal(data, &reqs); err != nil {
		s.logger.Warn("failed to parse change_requests.json", "error", err)
		return
	}
	s.requests = reqs
}

func (s *changeRequestStore) save() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.requests, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(), data, 0644)
}

func (s *changeRequestStore) List(status string) ([]ChangeRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ChangeRequest, 0, len(s.requests))
	for _, r := range s.requests {
		if status != "" && r.Status != status {
			continue
		}
		result = append(result, r)
	}
	// Newest first.
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result, nil
}

func (s *changeRequestStore) Get(id string) (*ChangeRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.requests {
		if r.ID == id {
			out := r
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "change request", Name: id}
}

func (s *changeRequestStore) Create(cr *ChangeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cr.ID == "" {
		cr.ID = fmt.Sprintf("cr-%d", time.Now().UnixNano())
	}
	if cr.CreatedAt == "" {
		cr.CreatedAt = time.Now().Format(time.RFC3339)
	}
	if cr.Status == "" {
		cr.Status = ChangeRequestPending
	}
	s.requests = append(s.requests, *cr)
	return s.save()
}

func (s *changeRequestStore) Update(cr *ChangeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.requests {
		if r.ID == cr.ID {
			s.requests[i] = *cr
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "change request", Name: cr.ID}
}

func (s *changeRequestStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.requests {
		if r.ID == id {
			s.requests = append(s.requests[:i], s.requests[i+1:]...)
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "change request", Name: id}
}
