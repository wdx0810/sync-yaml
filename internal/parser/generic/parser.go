package generic

import (
	"bytes"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/configmap-sync/configmap-sync/internal/k8s/gvr"
)

// Resource represents a parsed generic K8s resource.
type Resource struct {
	Object     *unstructured.Unstructured
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	GVR        schema.GroupVersionResource
	Namespaced bool
}

// Parser is the generic YAML parser interface.
type Parser interface {
	Parse(content []byte) (*Resource, error)
	ParseMulti(content []byte) ([]*Resource, error)
	Print(resource *Resource) ([]byte, error)
}

type parser struct {
	resolver gvr.Resolver
}

// NewParser creates a new generic parser.
func NewParser(resolver gvr.Resolver) Parser {
	return &parser{resolver: resolver}
}

// Parse parses a single YAML document into a Resource.
//
// We use sigs.k8s.io/yaml which routes via JSON, ensuring that numbers are
// decoded as float64/int64 (JSON-compatible) rather than yaml.v3's plain int.
// unstructured.Unstructured.DeepCopy explicitly does NOT support arbitrary Go
// types — it panics on `int` with "cannot deep copy int". This bit us when
// Service ports / Deployment replicas were parsed via gopkg.in/yaml.v3.
func (p *parser) Parse(content []byte) (*Resource, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, fmt.Errorf("empty document")
	}

	var raw map[string]interface{}
	if err := sigsyaml.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("yaml parse error: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("empty document")
	}

	obj := &unstructured.Unstructured{Object: raw}

	apiVersion := obj.GetAPIVersion()
	kind := obj.GetKind()
	name := obj.GetName()

	if apiVersion == "" {
		return nil, fmt.Errorf("validation failed: apiVersion is required")
	}
	if kind == "" {
		return nil, fmt.Errorf("validation failed: kind is required")
	}
	if name == "" {
		return nil, fmt.Errorf("validation failed: metadata.name is required")
	}

	gvrResult, namespaced, err := p.resolver.Resolve(apiVersion, kind)
	if err != nil {
		// Non-fatal: we can still represent the resource without GVR.
		gvrResult = schema.GroupVersionResource{}
		namespaced = true
	}

	return &Resource{
		Object:     obj,
		APIVersion: apiVersion,
		Kind:       kind,
		Name:       name,
		Namespace:  obj.GetNamespace(),
		GVR:        gvrResult,
		Namespaced: namespaced,
	}, nil
}

// ParseMulti parses a multi-document YAML (separated by `---`).
func (p *parser) ParseMulti(content []byte) ([]*Resource, error) {
	var resources []*Resource

	// Split on `---` lines. We can't use yaml.NewDecoder because that produces
	// gopkg.in/yaml.v3 typed values (which break unstructured DeepCopy).
	docs := splitYAMLDocs(content)
	for _, doc := range docs {
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		res, err := p.Parse(doc)
		if err != nil {
			// Skip individual invalid documents but keep parsing the rest.
			continue
		}
		resources = append(resources, res)
	}
	return resources, nil
}

// splitYAMLDocs splits a YAML stream into individual documents.
// Lines that consist solely of `---` (optionally with whitespace) are document separators.
func splitYAMLDocs(content []byte) [][]byte {
	var docs [][]byte
	var current []byte
	for _, line := range bytes.Split(content, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if bytes.Equal(trimmed, []byte("---")) {
			if len(bytes.TrimSpace(current)) > 0 {
				docs = append(docs, current)
			}
			current = nil
			continue
		}
		current = append(current, line...)
		current = append(current, '\n')
	}
	if len(bytes.TrimSpace(current)) > 0 {
		docs = append(docs, current)
	}
	return docs
}

// Print formats a Resource back to YAML.
func (p *parser) Print(resource *Resource) ([]byte, error) {
	return sigsyaml.Marshal(resource.Object.Object)
}
