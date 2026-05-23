package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// managedLabel is the label used to identify ConfigMaps managed by this system.
	managedLabel = "yaml-sync.io/managed"

	maxRetries = 3
	baseDelay  = 1 * time.Second
	maxDelay   = 30 * time.Second
	multiplier = 2.0
)

// Client defines the interface for Kubernetes interactions.
type Client interface {
	// ApplyConfigMap creates or updates a ConfigMap in the cluster.
	ApplyConfigMap(ctx context.Context, namespace string, cm *corev1.ConfigMap) error
	// GetConfigMap retrieves a ConfigMap from the cluster.
	GetConfigMap(ctx context.Context, namespace, name string) (*corev1.ConfigMap, error)
	// ListConfigMaps lists all managed ConfigMaps in the namespace.
	ListConfigMaps(ctx context.Context, namespace string) ([]*corev1.ConfigMap, error)
	// DeleteConfigMap removes a ConfigMap from the cluster.
	DeleteConfigMap(ctx context.Context, namespace, name string) error
	// WatchConfigMaps watches for ConfigMap changes in the namespace.
	WatchConfigMaps(ctx context.Context, namespace string) (<-chan *corev1.ConfigMap, error)
}

// clientImpl is the concrete implementation of Client using client-go.
type clientImpl struct {
	clientset kubernetes.Interface
	logger    *slog.Logger
}

// NewClient creates a new Kubernetes client.
// If kubeconfig is empty, it tries in-cluster config first, then falls back to default kubeconfig path.
func NewClient(kubeconfig string) (Client, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			config, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to build k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s clientset: %w", err)
	}

	return &clientImpl{
		clientset: clientset,
		logger:    slog.Default().With("component", "k8s"),
	}, nil
}

// NewClientFromClientset creates a Client from an existing kubernetes.Interface (useful for testing).
func NewClientFromClientset(cs kubernetes.Interface) Client {
	return &clientImpl{
		clientset: cs,
		logger:    slog.Default().With("component", "k8s"),
	}
}

// NewClientFromContent creates a Kubernetes client from kubeconfig YAML content.
func NewClientFromContent(kubeconfigContent string) (Client, error) {
	clientset, err := parseKubeconfigManual(kubeconfigContent)
	if err != nil {
		return nil, err
	}
	return &clientImpl{
		clientset: clientset,
		logger:    slog.Default().With("component", "k8s"),
	}, nil
}

// ApplyConfigMap creates or updates a ConfigMap with retry logic.
func (c *clientImpl) ApplyConfigMap(ctx context.Context, namespace string, cm *corev1.ConfigMap) error {
	return c.withRetry(ctx, "ApplyConfigMap", func() error {
		// Ensure the managed label is set.
		if cm.Labels == nil {
			cm.Labels = make(map[string]string)
		}
		cm.Labels[managedLabel] = "true"

		existing, err := c.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, cm.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			_, err = c.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}

		// Update existing ConfigMap.
		existing.Data = cm.Data
		existing.Labels = cm.Labels
		existing.Annotations = cm.Annotations
		_, err = c.clientset.CoreV1().ConfigMaps(namespace).Update(ctx, existing, metav1.UpdateOptions{})
		return err
	})
}

// GetConfigMap retrieves a ConfigMap with retry logic.
func (c *clientImpl) GetConfigMap(ctx context.Context, namespace, name string) (*corev1.ConfigMap, error) {
	var result *corev1.ConfigMap
	err := c.withRetry(ctx, "GetConfigMap", func() error {
		var err error
		result, err = c.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		return err
	})
	return result, err
}

// ListConfigMaps lists all managed ConfigMaps with retry logic.
func (c *clientImpl) ListConfigMaps(ctx context.Context, namespace string) ([]*corev1.ConfigMap, error) {
	var result []*corev1.ConfigMap
	err := c.withRetry(ctx, "ListConfigMaps", func() error {
		list, err := c.clientset.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		result = make([]*corev1.ConfigMap, len(list.Items))
		for i := range list.Items {
			result[i] = &list.Items[i]
		}
		return nil
	})
	return result, err
}

// DeleteConfigMap removes a ConfigMap with retry logic.
func (c *clientImpl) DeleteConfigMap(ctx context.Context, namespace, name string) error {
	return c.withRetry(ctx, "DeleteConfigMap", func() error {
		return c.clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
}

// WatchConfigMaps watches for ConfigMap changes and sends modified ConfigMaps to the channel.
func (c *clientImpl) WatchConfigMaps(ctx context.Context, namespace string) (<-chan *corev1.ConfigMap, error) {
	watcher, err := c.clientset.CoreV1().ConfigMaps(namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to watch configmaps: %w", err)
	}

	ch := make(chan *corev1.ConfigMap, 100)
	go func() {
		defer close(ch)
		defer watcher.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					return
				}
				if event.Type == "MODIFIED" || event.Type == "ADDED" {
					if cm, ok := event.Object.(*corev1.ConfigMap); ok {
						select {
						case ch <- cm:
						default:
							c.logger.Warn("watch channel full, dropping event", "name", cm.Name)
						}
					}
				}
			}
		}
	}()
	return ch, nil
}

// withRetry executes an operation with exponential backoff retry.
func (c *clientImpl) withRetry(ctx context.Context, operation string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Don't retry on context cancellation or non-retryable errors.
		if ctx.Err() != nil {
			return lastErr
		}
		if errors.IsNotFound(lastErr) || errors.IsForbidden(lastErr) || errors.IsUnauthorized(lastErr) {
			return lastErr
		}

		if attempt < maxRetries {
			delay := time.Duration(float64(baseDelay) * math.Pow(multiplier, float64(attempt)))
			if delay > maxDelay {
				delay = maxDelay
			}
			c.logger.Warn("retrying operation", "operation", operation, "attempt", attempt+1, "delay", delay, "error", lastErr)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return lastErr
			}
		}
	}

	c.logger.Error("operation failed after retries", "operation", operation, "maxRetries", maxRetries, "error", lastErr)
	return fmt.Errorf("%s failed after %d retries: %w", operation, maxRetries, lastErr)
}
