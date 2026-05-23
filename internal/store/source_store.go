package store

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/configmap-sync/configmap-sync/internal/crypto"
)

// SourceStore manages GitLab source persistence.
type SourceStore interface {
	List() ([]GitLabSource, error)
	Get(name string) (*GitLabSource, error)
	Create(source *GitLabSource) error
	Update(name string, source *GitLabSource) error
	Delete(name string) error
}

type sourceStore struct {
	sources     []GitLabSource
	storagePath string
	crypto      crypto.Service
	taskStore   func() TaskStore // lazy ref to avoid circular init
	mu          sync.RWMutex
	logger      *slog.Logger
}

// NewSourceStore creates a new SourceStore.
func NewSourceStore(storagePath string, cs crypto.Service, taskStoreRef func() TaskStore) SourceStore {
	s := &sourceStore{
		storagePath: storagePath,
		crypto:      cs,
		taskStore:   taskStoreRef,
		logger:      slog.Default().With("component", "source-store"),
	}
	s.load()
	return s
}

func (s *sourceStore) filePath() string {
	return filepath.Join(s.storagePath, "gitlab_sources.json")
}

func (s *sourceStore) load() {
	data, err := os.ReadFile(s.filePath())
	if err != nil {
		return
	}
	var sources []GitLabSource
	if err := json.Unmarshal(data, &sources); err != nil {
		s.logger.Warn("failed to parse gitlab_sources.json, starting empty", "error", err)
		return
	}
	s.sources = sources
}

func (s *sourceStore) save() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.sources, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(), data, 0600)
}

func (s *sourceStore) List() ([]GitLabSource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]GitLabSource, len(s.sources))
	copy(result, s.sources)
	return result, nil
}

func (s *sourceStore) Get(name string) (*GitLabSource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, src := range s.sources {
		if src.Name == name {
			out := src
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "source", Name: name}
}

func (s *sourceStore) Create(source *GitLabSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.sources {
		if existing.Name == source.Name {
			return &ErrDuplicate{Entity: "source", Name: source.Name}
		}
	}

	stored := *source
	if stored.Status == "" {
		stored.Status = "disconnected"
	}
	s.sources = append(s.sources, stored)
	return s.save()
}

func (s *sourceStore) Update(name string, source *GitLabSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.sources {
		if existing.Name == name {
			stored := *source
			stored.Name = name
			if source.Token == "" {
				stored.Token = existing.Token
			}
			if source.WebhookSecret == "" {
				stored.WebhookSecret = existing.WebhookSecret
			}
			if stored.Status == "" {
				stored.Status = existing.Status
			}
			s.sources[i] = stored
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "source", Name: name}
}

func (s *sourceStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check referential integrity.
	if s.taskStore != nil {
		if ts := s.taskStore(); ts != nil {
			tasks, _ := ts.List()
			var refs []string
			for _, t := range tasks {
				if t.SourceName == name {
					refs = append(refs, t.Name)
				}
			}
			if len(refs) > 0 {
				return &ErrReferenced{Entity: "source", Name: name, TaskNames: refs}
			}
		}
	}

	for i, existing := range s.sources {
		if existing.Name == name {
			s.sources = append(s.sources[:i], s.sources[i+1:]...)
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "source", Name: name}
}

func (s *sourceStore) encryptSafe(val string) string {
	if val == "" || s.crypto == nil {
		return val
	}
	enc, err := s.crypto.Encrypt(val)
	if err != nil {
		s.logger.Error("encryption failed", "error", err)
		return val
	}
	return enc
}

func (s *sourceStore) decryptSafe(val string) string {
	if val == "" || s.crypto == nil {
		return val
	}
	dec, err := s.crypto.Decrypt(val)
	if err != nil {
		s.logger.Error("decryption failed, data may be corrupted", "error", err)
		return "" // Return empty instead of corrupted ciphertext
	}
	return dec
}
