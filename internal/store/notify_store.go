package store

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// NotifyStore manages notification channels.
type NotifyStore interface {
	List() ([]NotifyChannel, error)
	Get(name string) (*NotifyChannel, error)
	Create(ch *NotifyChannel) error
	Update(name string, ch *NotifyChannel) error
	Delete(name string) error
}

type notifyStore struct {
	channels    []NotifyChannel
	storagePath string
	mu          sync.RWMutex
	logger      *slog.Logger
}

func NewNotifyStore(storagePath string) NotifyStore {
	s := &notifyStore{
		storagePath: storagePath,
		logger:      slog.Default().With("component", "notify-store"),
	}
	s.load()
	return s
}

func (s *notifyStore) filePath() string {
	return filepath.Join(s.storagePath, "notify_channels.json")
}

func (s *notifyStore) load() {
	data, err := os.ReadFile(s.filePath())
	if err != nil {
		return
	}
	var channels []NotifyChannel
	if err := json.Unmarshal(data, &channels); err != nil {
		s.logger.Warn("failed to parse notify_channels.json", "error", err)
		return
	}
	s.channels = channels
}

func (s *notifyStore) save() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.channels, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(), data, 0644)
}

func (s *notifyStore) List() ([]NotifyChannel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]NotifyChannel, len(s.channels))
	copy(result, s.channels)
	return result, nil
}

func (s *notifyStore) Get(name string) (*NotifyChannel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.channels {
		if ch.Name == name {
			out := ch
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "notify channel", Name: name}
}

func (s *notifyStore) Create(ch *NotifyChannel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.channels {
		if existing.Name == ch.Name {
			return &ErrDuplicate{Entity: "notify channel", Name: ch.Name}
		}
	}
	if ch.Type == "" {
		ch.Type = "feishu"
	}
	s.channels = append(s.channels, *ch)
	return s.save()
}

func (s *notifyStore) Update(name string, ch *NotifyChannel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.channels {
		if existing.Name == name {
			ch.Name = name
			s.channels[i] = *ch
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "notify channel", Name: name}
}

func (s *notifyStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.channels {
		if existing.Name == name {
			s.channels = append(s.channels[:i], s.channels[i+1:]...)
			return s.save()
		}
	}
	return &ErrNotFound{Entity: "notify channel", Name: name}
}
