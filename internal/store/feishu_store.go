package store

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/configmap-sync/configmap-sync/internal/crypto"
)

// FeishuConfig holds the Feishu (Lark) enterprise app integration settings.
// AppSecret is stored encrypted on disk.
type FeishuConfig struct {
	Enabled     bool   `json:"enabled"`
	AppID       string `json:"appId"`
	AppSecret   string `json:"appSecret"`
	RedirectURI string `json:"redirectUri"`
}

// FeishuStore persists the Feishu integration config (single record).
type FeishuStore interface {
	Get() (*FeishuConfig, error)          // masked secret (for display)
	GetWithSecret() (*FeishuConfig, error) // raw secret (for server-side use)
	Save(cfg *FeishuConfig) error
}

type feishuStore struct {
	cfg         FeishuConfig
	storagePath string
	crypto      crypto.Service
	mu          sync.RWMutex
	logger      *slog.Logger
}

func NewFeishuStore(storagePath string, cs crypto.Service) FeishuStore {
	s := &feishuStore{
		storagePath: storagePath,
		crypto:      cs,
		logger:      slog.Default().With("component", "feishu-store"),
	}
	s.load()
	return s
}

func (s *feishuStore) filePath() string {
	return filepath.Join(s.storagePath, "feishu.json")
}

func (s *feishuStore) load() {
	data, err := os.ReadFile(s.filePath())
	if err != nil {
		return
	}
	var cfg FeishuConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		s.logger.Warn("failed to parse feishu.json", "error", err)
		return
	}
	// Decrypt secret.
	cfg.AppSecret = s.decryptSafe(cfg.AppSecret)
	s.cfg = cfg
}

func (s *feishuStore) save() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	toStore := s.cfg
	toStore.AppSecret = s.encryptSafe(toStore.AppSecret)
	data, err := json.MarshalIndent(toStore, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(), data, 0600)
}

func (s *feishuStore) Get() (*FeishuConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.cfg
	if out.AppSecret != "" {
		out.AppSecret = "***"
	}
	return &out, nil
}

func (s *feishuStore) GetWithSecret() (*FeishuConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.cfg
	return &out, nil
}

func (s *feishuStore) Save(cfg *FeishuConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep existing secret if the incoming one is the masked placeholder or empty.
	if cfg.AppSecret == "" || cfg.AppSecret == "***" {
		cfg.AppSecret = s.cfg.AppSecret
	}
	s.cfg = *cfg
	return s.save()
}

func (s *feishuStore) encryptSafe(val string) string {
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

func (s *feishuStore) decryptSafe(val string) string {
	if val == "" || s.crypto == nil {
		return val
	}
	dec, err := s.crypto.Decrypt(val)
	if err != nil {
		s.logger.Error("decryption failed", "error", err)
		return ""
	}
	return dec
}
