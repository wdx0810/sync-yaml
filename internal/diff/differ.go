package diff

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/configmap-sync/configmap-sync/internal/k8s/cleaner"
)

// DiffResult holds the comparison result.
type DiffResult struct {
	HasDiff    bool        `json:"hasDiff"`
	FieldDiffs []FieldDiff `json:"fieldDiffs,omitempty"`
	OldYAML    string      `json:"oldYaml,omitempty"`
	NewYAML    string      `json:"newYaml,omitempty"`
}

// FieldDiff represents a single field difference.
type FieldDiff struct {
	Path     string `json:"path"`
	OldValue string `json:"oldValue,omitempty"`
	NewValue string `json:"newValue,omitempty"`
	Type     string `json:"type"` // "added", "modified", "deleted"
}

// Differ compares K8s resources ignoring runtime fields.
type Differ interface {
	Compare(old, new *unstructured.Unstructured) *DiffResult
}

type differ struct {
	cleaner cleaner.Cleaner
}

func NewDiffer(c cleaner.Cleaner) Differ {
	return &differ{cleaner: c}
}

func (d *differ) Compare(old, new *unstructured.Unstructured) *DiffResult {
	// Clean both objects.
	cleanOld := d.cleaner.Clean(old)
	cleanNew := d.cleaner.Clean(new)

	result := &DiffResult{}

	// Generate YAML for diff view.
	if oldBytes, err := yaml.Marshal(cleanOld.Object); err == nil {
		result.OldYAML = string(oldBytes)
	}
	if newBytes, err := yaml.Marshal(cleanNew.Object); err == nil {
		result.NewYAML = string(newBytes)
	}

	// Compare spec/data fields.
	oldSpec := extractComparableFields(cleanOld)
	newSpec := extractComparableFields(cleanNew)

	result.FieldDiffs = compareMap("", oldSpec, newSpec)
	result.HasDiff = len(result.FieldDiffs) > 0

	return result
}

func extractComparableFields(obj *unstructured.Unstructured) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range obj.Object {
		if k == "apiVersion" || k == "kind" || k == "metadata" {
			continue
		}
		result[k] = v
	}
	// Also include labels and annotations from metadata.
	if meta, ok := obj.Object["metadata"].(map[string]interface{}); ok {
		if labels, ok := meta["labels"]; ok {
			result["metadata.labels"] = labels
		}
		if annotations, ok := meta["annotations"]; ok {
			result["metadata.annotations"] = annotations
		}
	}
	return result
}

func compareMap(prefix string, old, new map[string]interface{}) []FieldDiff {
	var diffs []FieldDiff

	for k, nv := range new {
		path := joinPath(prefix, k)
		ov, exists := old[k]
		if !exists {
			diffs = append(diffs, FieldDiff{Path: path, NewValue: formatValue(nv), Type: "added"})
		} else if !reflect.DeepEqual(ov, nv) {
			// Check if both are maps for recursive comparison.
			oldMap, oldIsMap := ov.(map[string]interface{})
			newMap, newIsMap := nv.(map[string]interface{})
			if oldIsMap && newIsMap {
				diffs = append(diffs, compareMap(path, oldMap, newMap)...)
			} else {
				diffs = append(diffs, FieldDiff{Path: path, OldValue: formatValue(ov), NewValue: formatValue(nv), Type: "modified"})
			}
		}
	}

	for k, ov := range old {
		if _, exists := new[k]; !exists {
			path := joinPath(prefix, k)
			diffs = append(diffs, FieldDiff{Path: path, OldValue: formatValue(ov), Type: "deleted"})
		}
	}

	return diffs
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func formatValue(v interface{}) string {
	if v == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("%v", v)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// MaskSecretData replaces Secret data values with "***" for display.
func MaskSecretData(obj *unstructured.Unstructured) *unstructured.Unstructured {
	if obj.GetKind() != "Secret" {
		return obj
	}
	out := obj.DeepCopy()
	if data, ok := out.Object["data"].(map[string]interface{}); ok {
		for k := range data {
			data[k] = "***"
		}
	}
	if stringData, ok := out.Object["stringData"].(map[string]interface{}); ok {
		for k := range stringData {
			stringData[k] = "***"
		}
	}
	return out
}

// IsSameContent checks if two resources have the same content (ignoring runtime fields).
func IsSameContent(c cleaner.Cleaner, old, new *unstructured.Unstructured) bool {
	cleanOld := c.Clean(old)
	cleanNew := c.Clean(new)

	oldSpec := extractComparableFields(cleanOld)
	newSpec := extractComparableFields(cleanNew)

	return reflect.DeepEqual(oldSpec, newSpec)
}

// unused import guard
var _ = strings.TrimSpace
