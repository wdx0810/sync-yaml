package drift

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/configmap-sync/configmap-sync/internal/gitlab"
	"github.com/configmap-sync/configmap-sync/internal/k8s"
	"github.com/configmap-sync/configmap-sync/internal/parser"
)

// DriftAlert represents a detected drift between desired and actual state.
type DriftAlert struct {
	ID            string    `json:"id"`
	ConfigMapName string    `json:"configMapName"`
	Namespace     string    `json:"namespace"`
	DiffFields    []string  `json:"diffFields"`
	DetectedAt    time.Time `json:"detectedAt"`
	Status        string    `json:"status"` // "Pending", "Dismissed", "Resolved"
}

// Detector defines the interface for drift detection.
type Detector interface {
	Start(ctx context.Context) error
	Stop()
	GetAlerts() []DriftAlert
	DismissAlert(id string) error
	ResolveAlert(id string) error
}

// detectorImpl is the concrete implementation of Detector.
type detectorImpl struct {
	gitlabClient gitlab.Client
	k8sClient    k8s.Client
	namespace    string
	basePath     string
	interval     time.Duration
	alerts       map[string]*DriftAlert
	mu           sync.RWMutex
	cancel       context.CancelFunc
	logger       *slog.Logger
	nextID       int
}

// NewDetector creates a new Drift Detector.
func NewDetector(gc gitlab.Client, kc k8s.Client, namespace, basePath string, interval time.Duration) Detector {
	return &detectorImpl{
		gitlabClient: gc,
		k8sClient:    kc,
		namespace:    namespace,
		basePath:     basePath,
		interval:     interval,
		alerts:       make(map[string]*DriftAlert),
		logger:       slog.Default().With("component", "drift"),
	}
}

// Start begins the periodic drift detection loop.
func (d *detectorImpl) Start(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)
	go d.runLoop(ctx)
	d.logger.Info("drift detector started", "interval", d.interval)
	return nil
}

// Stop stops the drift detection loop.
func (d *detectorImpl) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// GetAlerts returns all alerts with "Pending" status.
func (d *detectorImpl) GetAlerts() []DriftAlert {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var pending []DriftAlert
	for _, a := range d.alerts {
		if a.Status == "Pending" {
			pending = append(pending, *a)
		}
	}
	return pending
}

// DismissAlert marks an alert as "Dismissed".
func (d *detectorImpl) DismissAlert(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	alert, ok := d.alerts[id]
	if !ok {
		return fmt.Errorf("alert %s not found", id)
	}
	alert.Status = "Dismissed"
	return nil
}

// ResolveAlert marks an alert as "Resolved".
func (d *detectorImpl) ResolveAlert(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	alert, ok := d.alerts[id]
	if !ok {
		return fmt.Errorf("alert %s not found", id)
	}
	alert.Status = "Resolved"
	return nil
}

// runLoop periodically checks for drift.
func (d *detectorImpl) runLoop(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.detectDrift(ctx); err != nil {
				d.logger.Error("drift detection failed", "error", err)
			}
		}
	}
}

// detectDrift compares desired state (GitLab) with actual state (K8s).
func (d *detectorImpl) detectDrift(ctx context.Context) error {
	// Fetch desired state from GitLab.
	files, err := d.gitlabClient.FetchFiles(ctx, d.basePath)
	if err != nil {
		return fmt.Errorf("failed to fetch files from gitlab: %w", err)
	}

	// Build desired state map.
	type cmKey struct{ namespace, name string }
	desiredMap := make(map[cmKey]map[string]string)

	for _, f := range files {
		cm, err := parser.Parse(f.Content)
		if err != nil {
			continue
		}
		ns := cm.Metadata.Namespace
		if ns == "" {
			ns = d.namespace
		}
		desiredMap[cmKey{ns, cm.Metadata.Name}] = cm.Data
	}

	// Compare with actual state in K8s.
	for key, desiredData := range desiredMap {
		actual, err := d.k8sClient.GetConfigMap(ctx, key.namespace, key.name)
		if err != nil {
			d.logger.Warn("failed to get configmap from k8s", "namespace", key.namespace, "name", key.name, "error", err)
			continue
		}

		diffFields := computeDiffFields(desiredData, actual.Data)
		if len(diffFields) > 0 {
			d.addOrUpdateAlert(key.namespace, key.name, diffFields)
		}
	}

	return nil
}

// computeDiffFields returns the list of fields that differ between desired and actual.
func computeDiffFields(desired, actual map[string]string) []string {
	var diffs []string

	for k, dv := range desired {
		av, ok := actual[k]
		if !ok {
			diffs = append(diffs, k+" (missing in cluster)")
		} else if dv != av {
			diffs = append(diffs, k+" (modified)")
		}
	}

	for k := range actual {
		if _, ok := desired[k]; !ok {
			diffs = append(diffs, k+" (extra in cluster)")
		}
	}

	return diffs
}

// addOrUpdateAlert creates or updates a drift alert.
func (d *detectorImpl) addOrUpdateAlert(namespace, name string, diffFields []string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if there's already a pending alert for this ConfigMap.
	for _, a := range d.alerts {
		if a.ConfigMapName == name && a.Namespace == namespace && a.Status == "Pending" {
			a.DiffFields = diffFields
			a.DetectedAt = time.Now()
			return
		}
	}

	d.nextID++
	id := fmt.Sprintf("drift-%d", d.nextID)
	d.alerts[id] = &DriftAlert{
		ID:            id,
		ConfigMapName: name,
		Namespace:     namespace,
		DiffFields:    diffFields,
		DetectedAt:    time.Now(),
		Status:        "Pending",
	}

	d.logger.Info("drift detected", "namespace", namespace, "name", name, "fields", diffFields)
}
