package history

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SyncRecord represents a single synchronization operation record.
type SyncRecord struct {
	ID            string       `json:"id"`
	Timestamp     time.Time    `json:"timestamp"`
	TaskName      string       `json:"taskName,omitempty"`
	ConfigMapName string       `json:"configMapName"`
	Namespace     string       `json:"namespace"`
	Direction     string       `json:"direction"`
	ChangeType    string       `json:"changeType"`
	Status        string       `json:"status"`
	BeforeSummary string       `json:"beforeSummary"`
	AfterSummary  string       `json:"afterSummary"`
	ErrorMessage  string       `json:"errorMessage,omitempty"`
	Details       []ChangeDetail `json:"details,omitempty"`
}

// ChangeDetail records what changed in a specific ConfigMap.
type ChangeDetail struct {
	Name      string        `json:"name"`
	Namespace string        `json:"namespace"`
	Kind      string        `json:"kind,omitempty"`
	Group     string        `json:"group,omitempty"`
	Action    string        `json:"action"`
	OldYAML   string        `json:"oldYaml,omitempty"`
	NewYAML   string        `json:"newYaml,omitempty"`
	Changes   []FieldChange `json:"changes,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// FieldChange records a single field change.
type FieldChange struct {
	Key      string `json:"key"`
	OldValue string `json:"oldValue,omitempty"`
	NewValue string `json:"newValue,omitempty"`
	Type     string `json:"type"` // "added", "modified", "deleted"
}

// QueryFilter defines the filter criteria for querying sync records.
type QueryFilter struct {
	Name      string
	Namespace string
	Direction string // "forward" | "reverse"
	Since     *time.Time
	Until     *time.Time
	Page      int // 1-based, 0 means no pagination
	PageSize  int // default 50
}

// QueryResult wraps paginated query results.
type QueryResult struct {
	Records []SyncRecord `json:"records"`
	Total   int          `json:"total"`
	Page    int          `json:"page"`
	PageSize int         `json:"pageSize"`
}

// Store defines the interface for history record storage.
type Store interface {
	Save(record *SyncRecord) error
	Query(filter QueryFilter) (*QueryResult, error)
	Flush() error
}

// fileStore is the concrete implementation of Store using JSON file storage.
type fileStore struct {
	storagePath string
	records     []SyncRecord
	pending     []SyncRecord // records that failed to write to disk
	mu          sync.RWMutex
	logger      *slog.Logger
}

// NewStore creates a new file-based History Store.
func NewStore(storagePath string) (Store, error) {
	// Expand ~ to home directory.
	if len(storagePath) > 0 && storagePath[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		storagePath = filepath.Join(home, storagePath[1:])
	}

	// Ensure storage directory exists.
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	s := &fileStore{
		storagePath: storagePath,
		logger:      slog.Default().With("component", "history"),
	}

	// Load existing records.
	if err := s.loadRecords(); err != nil {
		s.logger.Warn("failed to load existing records", "error", err)
	}

	// Cleanup records older than 30 days.
	s.cleanup30Days()

	return s, nil
}

// Save persists a sync record to the store.
func (s *fileStore) Save(record *SyncRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.records = append(s.records, *record)

	// Periodically clean old records (every 100 saves).
	if len(s.records)%100 == 0 {
		s.cleanupLocked()
	}

	if err := s.writeRecords(); err != nil {
		s.logger.Error("failed to write record to disk, caching in memory", "error", err)
		s.pending = append(s.pending, *record)
		return nil
	}

	return nil
}

// Query retrieves records matching the filter criteria, with pagination.
func (s *fileStore) Query(filter QueryFilter) (*QueryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []SyncRecord
	for _, r := range s.records {
		if !matchesFilter(r, filter) {
			continue
		}
		results = append(results, r)
	}

	// Sort by timestamp descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	total := len(results)

	// Apply pagination.
	page := filter.Page
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if page <= 0 {
		page = 1
	}

	start := (page - 1) * pageSize
	if start >= total {
		return &QueryResult{Records: []SyncRecord{}, Total: total, Page: page, PageSize: pageSize}, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return &QueryResult{
		Records:  results[start:end],
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// Flush writes any in-memory cached records to disk.
func (s *fileStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) == 0 {
		return nil
	}

	if err := s.writeRecords(); err != nil {
		return fmt.Errorf("flush failed: %w", err)
	}

	s.logger.Info("flushed pending records to disk", "count", len(s.pending))
	s.pending = nil
	return nil
}

// matchesFilter checks if a record matches the given filter criteria.
func matchesFilter(r SyncRecord, f QueryFilter) bool {
	if f.Name != "" && r.ConfigMapName != f.Name {
		return false
	}
	if f.Namespace != "" && r.Namespace != f.Namespace {
		return false
	}
	if f.Direction != "" && r.Direction != f.Direction {
		return false
	}
	if f.Since != nil && r.Timestamp.Before(*f.Since) {
		return false
	}
	if f.Until != nil && r.Timestamp.After(*f.Until) {
		return false
	}
	return true
}

// dataFilePath returns the path to the JSON data file.
func (s *fileStore) dataFilePath() string {
	return filepath.Join(s.storagePath, "history.json")
}

// loadRecords loads existing records from the JSON file.
func (s *fileStore) loadRecords() error {
	data, err := os.ReadFile(s.dataFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read history file: %w", err)
	}

	if err := json.Unmarshal(data, &s.records); err != nil {
		return fmt.Errorf("failed to parse history file: %w", err)
	}

	return nil
}

// writeRecords writes all records to the JSON file.
func (s *fileStore) writeRecords() error {
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal records: %w", err)
	}

	if err := os.WriteFile(s.dataFilePath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write history file: %w", err)
	}

	return nil
}

// cleanup30Days removes records older than 30 days (thread-safe wrapper).
func (s *fileStore) cleanup30Days() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
}

// cleanupLocked removes records older than 30 days. Must be called with lock held.
func (s *fileStore) cleanupLocked() {
	cutoff := time.Now().AddDate(0, 0, -30)
	var kept []SyncRecord
	removed := 0
	for _, r := range s.records {
		if r.Timestamp.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	if removed > 0 {
		s.records = kept
		_ = s.writeRecords()
		s.logger.Info("cleaned old history records", "removed", removed, "remaining", len(kept))
	}
}
