package dynamic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	k8sdynamic "k8s.io/client-go/dynamic"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"
)

const (
	maxRetries = 2
	baseDelay  = 500 * time.Millisecond
	maxDelay   = 5 * time.Second
	multiplier = 2.0
	fieldManager = "yaml-sync"
)

// Client is the generic dynamic K8s client interface.
type Client interface {
	Apply(ctx context.Context, namespace string, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error
	Get(ctx context.Context, namespace string, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error)
	List(ctx context.Context, namespace string, gvr schema.GroupVersionResource, labelSelector string) ([]*unstructured.Unstructured, error)
	Delete(ctx context.Context, namespace string, gvr schema.GroupVersionResource, name string) error
	Watch(ctx context.Context, namespace string, gvr schema.GroupVersionResource) (watch.Interface, error)
}

type client struct {
	dynamic k8sdynamic.Interface
	logger  *slog.Logger
}

// NewClient creates a new dynamic client from a k8sdynamic.Interface.
func NewClient(dynClient k8sdynamic.Interface) Client {
	return &client{
		dynamic: dynClient,
		logger:  slog.Default().With("component", "dynamic-client"),
	}
}

func (c *client) resource(namespace string, gvr schema.GroupVersionResource) k8sdynamic.ResourceInterface {
	if namespace != "" {
		return c.dynamic.Resource(gvr).Namespace(namespace)
	}
	return c.dynamic.Resource(gvr)
}

// Apply uses Server-Side Apply to apply a resource.
func (c *client) Apply(ctx context.Context, namespace string, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	return c.withRetry(ctx, "Apply", func() error {
		// Deep copy to avoid mutating the original object.
		applyObj := obj.DeepCopy()

		// Remove fields that conflict with Server-Side Apply.
		unstructured.RemoveNestedField(applyObj.Object, "metadata", "resourceVersion")
		unstructured.RemoveNestedField(applyObj.Object, "metadata", "uid")
		unstructured.RemoveNestedField(applyObj.Object, "metadata", "creationTimestamp")
		unstructured.RemoveNestedField(applyObj.Object, "metadata", "generation")
		unstructured.RemoveNestedField(applyObj.Object, "metadata", "managedFields")
		unstructured.RemoveNestedField(applyObj.Object, "metadata", "selfLink")
		// Drop ownerReferences — when applying YAML from GitLab the original owner
		// may not exist (or be a different UID) in the target cluster.
		unstructured.RemoveNestedField(applyObj.Object, "metadata", "ownerReferences")
		unstructured.RemoveNestedField(applyObj.Object, "status")

		// Drop kind-specific immutable / server-injected fields. Stale values
		// from a previous cluster (e.g. Service.spec.clusterIP) make Apply fail
		// with "field is immutable".
		switch applyObj.GetKind() {
		case "Service":
			unstructured.RemoveNestedField(applyObj.Object, "spec", "clusterIP")
			unstructured.RemoveNestedField(applyObj.Object, "spec", "clusterIPs")
			if ports, found, _ := unstructured.NestedSlice(applyObj.Object, "spec", "ports"); found {
				for _, p := range ports {
					if pm, ok := p.(map[string]interface{}); ok {
						delete(pm, "nodePort")
					}
				}
				_ = unstructured.SetNestedSlice(applyObj.Object, ports, "spec", "ports")
			}
		case "ServiceAccount":
			unstructured.RemoveNestedField(applyObj.Object, "secrets")
		case "PersistentVolumeClaim":
			unstructured.RemoveNestedField(applyObj.Object, "spec", "volumeName")
		case "Pod":
			unstructured.RemoveNestedField(applyObj.Object, "spec", "nodeName")
			unstructured.RemoveNestedField(applyObj.Object, "spec", "serviceAccount")
		}

		data, err := json.Marshal(applyObj)
		if err != nil {
			return fmt.Errorf("marshal failed: %w", err)
		}
		_, err = c.resource(namespace, gvr).Patch(ctx, applyObj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
			FieldManager: fieldManager,
			Force:        ptr.To(true),
		})
		return err
	})
}

// Get retrieves a resource by name.
func (c *client) Get(ctx context.Context, namespace string, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	var result *unstructured.Unstructured
	err := c.withRetry(ctx, "Get", func() error {
		var err error
		result, err = c.resource(namespace, gvr).Get(ctx, name, metav1.GetOptions{})
		return err
	})
	return result, err
}

// List lists all resources of a type in a namespace.
func (c *client) List(ctx context.Context, namespace string, gvr schema.GroupVersionResource, labelSelector string) ([]*unstructured.Unstructured, error) {
	var result []*unstructured.Unstructured
	err := c.withRetry(ctx, "List", func() error {
		list, err := c.resource(namespace, gvr).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return err
		}
		result = make([]*unstructured.Unstructured, len(list.Items))
		for i := range list.Items {
			result[i] = &list.Items[i]
		}
		return nil
	})
	return result, err
}

// Delete removes a resource.
func (c *client) Delete(ctx context.Context, namespace string, gvr schema.GroupVersionResource, name string) error {
	return c.withRetry(ctx, "Delete", func() error {
		return c.resource(namespace, gvr).Delete(ctx, name, metav1.DeleteOptions{})
	})
}

// Watch starts watching for resource changes.
func (c *client) Watch(ctx context.Context, namespace string, gvr schema.GroupVersionResource) (watch.Interface, error) {
	return c.resource(namespace, gvr).Watch(ctx, metav1.ListOptions{})
}

func (c *client) withRetry(ctx context.Context, op string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return lastErr
		}
		// Don't retry on errors that won't change with retry.
		if errors.IsNotFound(lastErr) ||
			errors.IsForbidden(lastErr) ||
			errors.IsUnauthorized(lastErr) ||
			errors.IsInvalid(lastErr) ||
			errors.IsBadRequest(lastErr) ||
			errors.IsConflict(lastErr) {
			return lastErr
		}
		if attempt < maxRetries {
			delay := time.Duration(float64(baseDelay) * math.Pow(multiplier, float64(attempt)))
			if delay > maxDelay {
				delay = maxDelay
			}
			c.logger.Warn("retrying", "op", op, "attempt", attempt+1, "error", lastErr)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return lastErr
			}
		}
	}
	return fmt.Errorf("%s failed after %d retries: %w", op, maxRetries, lastErr)
}
