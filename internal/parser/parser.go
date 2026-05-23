package parser

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigMapData represents a parsed Kubernetes ConfigMap.
type ConfigMapData struct {
	APIVersion string            `yaml:"apiVersion" json:"apiVersion"`
	Kind       string            `yaml:"kind" json:"kind"`
	Metadata   Metadata          `yaml:"metadata" json:"metadata"`
	Data       map[string]string `yaml:"data" json:"data"`
}

// Metadata holds ConfigMap metadata fields.
type Metadata struct {
	Name        string            `yaml:"name" json:"name"`
	Namespace   string            `yaml:"namespace" json:"namespace"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// Parse unmarshals YAML bytes into a ConfigMapData and validates the result.
// Returns an error with line information for YAML syntax errors.
func Parse(content []byte) (*ConfigMapData, error) {
	var cm ConfigMapData
	if err := yaml.Unmarshal(content, &cm); err != nil {
		return nil, formatYAMLError(err)
	}
	if err := Validate(&cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

// Validate checks that the ConfigMapData has the required fields for a valid
// Kubernetes ConfigMap: apiVersion must be "v1", kind must be "ConfigMap",
// metadata.name must be non-empty, and data must not be nil.
func Validate(cm *ConfigMapData) error {
	var errs []string

	if cm.APIVersion != "v1" {
		errs = append(errs, fmt.Sprintf("apiVersion must be \"v1\", got %q", cm.APIVersion))
	}
	if cm.Kind != "ConfigMap" {
		errs = append(errs, fmt.Sprintf("kind must be \"ConfigMap\", got %q", cm.Kind))
	}
	if cm.Metadata.Name == "" {
		errs = append(errs, "metadata.name is required")
	}
	if cm.Data == nil {
		errs = append(errs, "data is required")
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Print marshals a ConfigMapData back to YAML bytes.
func Print(cm *ConfigMapData) ([]byte, error) {
	return yaml.Marshal(cm)
}

// formatYAMLError wraps a yaml parsing error to include line information
// when available.
func formatYAMLError(err error) error {
	// yaml.v3 already includes line/column info in its error messages,
	// e.g. "yaml: line 3: did not find expected key"
	return fmt.Errorf("yaml parse error: %w", err)
}
