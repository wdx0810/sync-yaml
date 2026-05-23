package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// GitLabConfig holds GitLab connection settings.
type GitLabConfig struct {
	URL           string `yaml:"url"`
	Token         string `yaml:"token"`
	ProjectID     int    `yaml:"projectId"`
	Branch        string `yaml:"branch"`
	Path          string `yaml:"path"`
	WebhookSecret string `yaml:"webhookSecret"`
}

// K8sConfig holds Kubernetes connection settings.
type K8sConfig struct {
	Kubeconfig string `yaml:"kubeconfig"`
	Namespace  string `yaml:"namespace"`
}

// SyncConfig holds synchronization mode and interval settings.
type SyncConfig struct {
	Mode     string `yaml:"mode"`
	Interval int    `yaml:"interval"`
}

// DriftConfig holds drift detection settings.
type DriftConfig struct {
	Interval int `yaml:"interval"`
}

// HistoryConfig holds history storage settings.
type HistoryConfig struct {
	StoragePath string `yaml:"storagePath"`
}

// APIConfig holds HTTP API server settings.
type APIConfig struct {
	Port int `yaml:"port"`
}

// Config is the top-level configuration for the YAML sync system.
type Config struct {
	GitLab  GitLabConfig  `yaml:"gitlab"`
	K8s     K8sConfig     `yaml:"k8s"`
	Sync    SyncConfig    `yaml:"sync"`
	Drift   DriftConfig   `yaml:"drift"`
	History HistoryConfig `yaml:"history"`
	API     APIConfig     `yaml:"api"`
}

// applyDefaults sets default values for fields that are zero-valued.
func (c *Config) applyDefaults() {
	if c.GitLab.Branch == "" {
		c.GitLab.Branch = "main"
	}
	if c.GitLab.Path == "" {
		c.GitLab.Path = "/"
	}
	if c.K8s.Namespace == "" {
		c.K8s.Namespace = "default"
	}
	if c.Sync.Mode == "" {
		c.Sync.Mode = "manual"
	}
	if c.Sync.Interval == 0 {
		c.Sync.Interval = 300
	}
	if c.Drift.Interval == 0 {
		c.Drift.Interval = 60
	}
	if c.History.StoragePath == "" {
		c.History.StoragePath = "/data"
	}
	if c.API.Port == 0 {
		c.API.Port = 8080
	}
}

// Validate checks that the configuration is valid.
// GitLab and K8s settings are optional — they can be configured later via the frontend.
func (c *Config) Validate() error {
	switch c.Sync.Mode {
	case "auto", "scheduled", "manual":
		// valid
	default:
		return fmt.Errorf("sync.mode must be one of: auto, scheduled, manual; got %q", c.Sync.Mode)
	}

	if c.Sync.Mode == "scheduled" && c.Sync.Interval < 30 {
		return fmt.Errorf("sync.interval must be >= 30 seconds when sync.mode is \"scheduled\"; got %d", c.Sync.Interval)
	}

	return nil
}

// ValidateGitLab checks GitLab-specific configuration when a connection is being established.
func (c *Config) ValidateGitLab() error {
	if c.GitLab.URL == "" {
		return fmt.Errorf("gitlab.url is required")
	}
	if c.GitLab.Token == "" {
		return fmt.Errorf("gitlab.token is required")
	}
	if c.Sync.Mode == "auto" && c.GitLab.WebhookSecret == "" {
		return fmt.Errorf("gitlab.webhookSecret is required when sync.mode is \"auto\"")
	}
	return nil
}

// HasGitLab returns true if GitLab connection is configured.
func (c *Config) HasGitLab() bool {
	return c.GitLab.URL != "" && c.GitLab.Token != ""
}

// HasK8s returns true if K8s connection info is available.
func (c *Config) HasK8s() bool {
	return c.K8s.Kubeconfig != "" || c.K8s.Namespace != ""
}

// LoadConfig loads configuration from a YAML file at the given path.
// If path is empty, a Config with default values is returned.
// Defaults are applied before validation.
func LoadConfig(path string) (*Config, error) {
	cfg := &Config{}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}
