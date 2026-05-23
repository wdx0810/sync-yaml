package store

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/configmap-sync/configmap-sync/internal/crypto"
)

// TargetStore manages K8s target persistence.
type TargetStore interface {
	List() ([]K8sTarget, error)
	Get(name string) (*K8sTarget, error)
	Create(target *K8sTarget) error
	Update(name string, target *K8sTarget) error
	Delete(name string) error
}

type targetStore struct {
	targets     []K8sTarget
	storagePath string
	crypto      crypto.Service
	taskStore   func() TaskStore
	mu          sync.RWMutex
	logger      *slog.Logger
}

func NewTargetStore(storagePath string, cs crypto.Service, taskStoreRef func() TaskStore) TargetStore {
	s := &targetStore{
		storagePath: storagePath,
		crypto:      cs,
		taskStore:   taskStoreRef,
		logger:      slog.Default().With("component", "target-store"),
	}
	s.load()
	return s
}

func (s *targetStore) filePath() string {
	return filepath.Join(s.storagePath, "k8s_targets.json")
}

func (s *targetStore) load() {
	data, err := os.ReadFile(s.filePath())
	if err != nil {
		return
	}
	var targets []K8sTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		s.logger.Warn("failed to parse k8s_targets.json, starting empty", "error", err)
		return
	}
	s.targets = targets
}

func (s *targetStore) save() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.targets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(), data, 0600)
}

func (s *targetStore) List() ([]K8sTarget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]K8sTarget, len(s.targets))
	copy(result, s.targets)
	return result, nil
}

func (s *targetStore) Get(name string) (*K8sTarget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.targets {
		if t.Name == name {
			out := t
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "target", Name: name}
}

func (s *targetStore) Create(target *K8sTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.targets {
		if existing.Name == target.Name {
			return &ErrDuplicate{Entity: "target", Name: target.Name}
		}
	}
	stored := *target
	if stored.Status == "" {
		stored.Status = "disconnected"
	}
	s.targets = append(s.targets, stored)
	return s.save()
}

func (s *targetStore) Update(name string, target *K8sTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.targets {
		if existing.Name == name {
			stored := *target
			stored.Name = name
			if target.KubeconfigContent == "" {
				stored.KubeconfigContent = existing.KubeconfigContent
			}
			if stored.Status == "" {
				stored.Status = existing.Status
			}
			s.targets[i] = stored
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "target", Name: name}
}

func (s *targetStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taskStore != nil {
		if ts := s.taskStore(); ts != nil {
			tasks, _ := ts.List()
			var refs []string
			for _, t := range tasks {
				if t.TargetName == name {
					refs = append(refs, t.Name)
				}
			}
			if len(refs) > 0 {
				return &ErrReferenced{Entity: "target", Name: name, TaskNames: refs}
			}
		}
	}
	for i, existing := range s.targets {
		if existing.Name == name {
			s.targets = append(s.targets[:i], s.targets[i+1:]...)
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "target", Name: name}
}

func (s *targetStore) encryptSafe(val string) string {
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

func (s *targetStore) decryptSafe(val string) string {
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
