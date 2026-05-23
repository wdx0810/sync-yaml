package gvr

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// Resolver maps apiVersion/kind to GroupVersionResource.
type Resolver interface {
	Resolve(apiVersion, kind string) (schema.GroupVersionResource, bool, error)
	IsNamespaced(gvr schema.GroupVersionResource) bool
	RefreshCache() error
}

// ResourceInfo holds GVR and scope info.
type ResourceInfo struct {
	GVR        schema.GroupVersionResource
	Namespaced bool
}

type resolver struct {
	discovery   discovery.DiscoveryInterface
	cache       map[string]*ResourceInfo
	cacheMu     sync.RWMutex
	cacheExpiry time.Time
	cacheTTL    time.Duration
}

// NewResolver creates a GVR resolver. discovery can be nil (builtin-only mode).
func NewResolver(disc discovery.DiscoveryInterface) Resolver {
	return &resolver{
		discovery: disc,
		cache:     make(map[string]*ResourceInfo),
		cacheTTL:  5 * time.Minute,
	}
}

func (r *resolver) Resolve(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	key := apiVersion + "/" + kind

	// Check builtin mapping first.
	if info, ok := builtinMapping[key]; ok {
		return info.GVR, info.Namespaced, nil
	}

	// Check discovery cache.
	r.cacheMu.RLock()
	if info, ok := r.cache[key]; ok && time.Now().Before(r.cacheExpiry) {
		r.cacheMu.RUnlock()
		return info.GVR, info.Namespaced, nil
	}
	r.cacheMu.RUnlock()

	// Query discovery API.
	if r.discovery == nil {
		return schema.GroupVersionResource{}, false, fmt.Errorf("unsupported resource type: %s/%s", apiVersion, kind)
	}

	if err := r.refreshCacheIfNeeded(); err != nil {
		return schema.GroupVersionResource{}, false, fmt.Errorf("discovery failed: %w", err)
	}

	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	if info, ok := r.cache[key]; ok {
		return info.GVR, info.Namespaced, nil
	}

	return schema.GroupVersionResource{}, false, fmt.Errorf("unsupported resource type: %s/%s", apiVersion, kind)
}

func (r *resolver) IsNamespaced(gvr schema.GroupVersionResource) bool {
	for _, info := range builtinMapping {
		if info.GVR == gvr {
			return info.Namespaced
		}
	}
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	for _, info := range r.cache {
		if info.GVR == gvr {
			return info.Namespaced
		}
	}
	return true // default to namespaced
}

func (r *resolver) RefreshCache() error {
	return r.refreshCacheIfNeeded()
}

func (r *resolver) refreshCacheIfNeeded() error {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	if time.Now().Before(r.cacheExpiry) {
		return nil
	}

	if r.discovery == nil {
		return nil
	}

	_, apiResourceLists, err := r.discovery.ServerGroupsAndResources()
	if err != nil {
		return err
	}

	for _, list := range apiResourceLists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, res := range list.APIResources {
			if strings.Contains(res.Name, "/") {
				continue // skip subresources
			}
			key := list.GroupVersion + "/" + res.Kind
			r.cache[key] = &ResourceInfo{
				GVR:        schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: res.Name},
				Namespaced: res.Namespaced,
			}
		}
	}

	r.cacheExpiry = time.Now().Add(r.cacheTTL)
	return nil
}

// KindToPlural returns the plural form of a resource kind.
func KindToPlural(kind string) string {
	if p, ok := kindPluralMap[kind]; ok {
		return p
	}
	return strings.ToLower(kind) + "s"
}

var kindPluralMap = map[string]string{
	"ConfigMap":                "configmaps",
	"Secret":                   "secrets",
	"Service":                  "services",
	"ServiceAccount":           "serviceaccounts",
	"Deployment":               "deployments",
	"StatefulSet":              "statefulsets",
	"DaemonSet":                "daemonsets",
	"CronJob":                  "cronjobs",
	"Job":                      "jobs",
	"Ingress":                  "ingresses",
	"NetworkPolicy":            "networkpolicies",
	"PersistentVolumeClaim":    "persistentvolumeclaims",
	"Role":                     "roles",
	"RoleBinding":              "rolebindings",
	"ClusterRole":              "clusterroles",
	"ClusterRoleBinding":       "clusterrolebindings",
	"HorizontalPodAutoscaler":  "horizontalpodautoscalers",
	"Namespace":                "namespaces",
	"Pod":                      "pods",
	"ReplicaSet":               "replicasets",
}

// builtinMapping contains pre-defined GVR mappings for common resources.
var builtinMapping = map[string]*ResourceInfo{
	"v1/ConfigMap":             {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}, Namespaced: true},
	"v1/Secret":                {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, Namespaced: true},
	"v1/Service":               {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, Namespaced: true},
	"v1/ServiceAccount":        {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}, Namespaced: true},
	"v1/PersistentVolumeClaim": {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}, Namespaced: true},
	"v1/Namespace":             {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}, Namespaced: false},
	"v1/Pod":                   {GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, Namespaced: true},
	"apps/v1/Deployment":       {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Namespaced: true},
	"apps/v1/StatefulSet":      {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, Namespaced: true},
	"apps/v1/DaemonSet":        {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, Namespaced: true},
	"apps/v1/ReplicaSet":       {GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, Namespaced: true},
	"batch/v1/CronJob":         {GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, Namespaced: true},
	"batch/v1/Job":             {GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespaced: true},
	"networking.k8s.io/v1/Ingress":       {GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, Namespaced: true},
	"networking.k8s.io/v1/NetworkPolicy": {GVR: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, Namespaced: true},
	"rbac.authorization.k8s.io/v1/Role":               {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, Namespaced: true},
	"rbac.authorization.k8s.io/v1/RoleBinding":        {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, Namespaced: true},
	"rbac.authorization.k8s.io/v1/ClusterRole":        {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, Namespaced: false},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding": {GVR: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}, Namespaced: false},
	"autoscaling/v2/HorizontalPodAutoscaler":          {GVR: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}, Namespaced: true},
}
