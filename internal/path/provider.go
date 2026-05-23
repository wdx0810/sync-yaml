package path

import (
	"fmt"
	"strings"
)

// Provider generates and parses file paths for resources in GitLab.
type Provider interface {
	ResourcePath(basePath, namespace, resourceTypePlural, name string, isNamespaced bool) string
	ParsePath(filePath, basePath string) (namespace, resourceType, name string, err error)
}

type provider struct{}

func NewProvider() Provider {
	return &provider{}
}

// ResourcePath generates the GitLab file path for a resource.
// Namespaced: {basePath}/{namespace}/{resourceTypePlural}/{name}.yaml
// Cluster-scoped: {basePath}/_cluster/{resourceTypePlural}/{name}.yaml
func (p *provider) ResourcePath(basePath, namespace, resourceTypePlural, name string, isNamespaced bool) string {
	basePath = strings.TrimPrefix(strings.TrimSuffix(basePath, "/"), "/")
	safeName := sanitizeFileName(name)

	var path string
	if !isNamespaced {
		if basePath == "" {
			path = fmt.Sprintf("_cluster/%s/%s.yaml", resourceTypePlural, safeName)
		} else {
			path = fmt.Sprintf("%s/_cluster/%s/%s.yaml", basePath, resourceTypePlural, safeName)
		}
	} else {
		if basePath == "" {
			path = fmt.Sprintf("%s/%s/%s.yaml", namespace, resourceTypePlural, safeName)
		} else {
			path = fmt.Sprintf("%s/%s/%s/%s.yaml", basePath, namespace, resourceTypePlural, safeName)
		}
	}
	return path
}

// ParsePath extracts namespace, resource type, and name from a file path.
func (p *provider) ParsePath(filePath, basePath string) (namespace, resourceType, name string, err error) {
	basePath = strings.TrimPrefix(strings.TrimSuffix(basePath, "/"), "/")
	rel := filePath
	if basePath != "" {
		rel = strings.TrimPrefix(filePath, basePath+"/")
	}

	parts := strings.Split(rel, "/")

	// New format: namespace/resourceType/name.yaml or _cluster/resourceType/name.yaml
	if len(parts) == 3 {
		ns := parts[0]
		resType := parts[1]
		fileName := parts[2]
		n := strings.TrimSuffix(fileName, ".yaml")
		n = strings.TrimSuffix(n, ".yml")
		if ns == "_cluster" {
			return "", resType, n, nil
		}
		return ns, resType, n, nil
	}

	// Old format: namespace/name.yaml (backward compat)
	if len(parts) == 2 {
		ns := parts[0]
		fileName := parts[1]
		n := strings.TrimSuffix(fileName, ".yaml")
		n = strings.TrimSuffix(n, ".yml")
		return ns, "", n, nil
	}

	return "", "", "", fmt.Errorf("cannot parse path: %s", filePath)
}

func sanitizeFileName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(name)
}
