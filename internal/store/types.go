package store

import "fmt"

// GitLabSource represents a GitLab data source configuration.
type GitLabSource struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Token         string `json:"token"`
	ProjectID     int    `json:"projectId"`
	Branch        string `json:"branch"`
	Path          string `json:"path"`
	WebhookSecret string `json:"webhookSecret,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
}

// K8sTarget represents a Kubernetes cluster target configuration.
type K8sTarget struct {
	Name              string `json:"name"`
	KubeconfigContent string `json:"kubeconfigContent"`
	Namespace         string `json:"namespace"`
	Status            string `json:"status"`
	Error             string `json:"error,omitempty"`
}

// SyncTask represents a synchronization task.
type SyncTask struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Project        string   `json:"project,omitempty"`
	SourceName     string   `json:"sourceName"`
	TargetName     string   `json:"targetName"`
	SourcePath     string   `json:"sourcePath,omitempty"`
	TargetNS       string   `json:"targetNamespace,omitempty"`
	Direction      string   `json:"direction"`
	SyncMode       string   `json:"syncMode"`
	Interval       int      `json:"interval"`
	ResourceTypes  []string `json:"resourceTypes,omitempty"`
	IncludeFilter  string   `json:"includeFilter,omitempty"`
	ExcludeFilter  string   `json:"excludeFilter,omitempty"`
	NotifyChannel  string   `json:"notifyChannel,omitempty"`
	WebhookToken   string   `json:"webhookToken,omitempty"`
	Status         string   `json:"status"`
	LastSyncTime   string   `json:"lastSyncTime"`
	LastSyncResult string   `json:"lastSyncResult"`
	ErrorMessage   string   `json:"errorMessage,omitempty"`
}

// ChangeRequest represents a developer's request to modify a ConfigMap and
// have it committed to GitLab after approval. This module is fully independent
// of the sync engine — approving a request only writes to GitLab; it never
// touches K8s or the sync tasks.
type ChangeRequest struct {
	ID          string `json:"id"`
	TaskID      string `json:"taskId"`      // related sync task (locates the GitLab source + path / environment)
	TaskName    string `json:"taskName"`    // denormalized for display
	Project     string `json:"project,omitempty"`
	Namespace   string `json:"namespace"`   // ConfigMap namespace
	Name        string `json:"name"`        // ConfigMap name
	FilePath    string `json:"filePath"`    // resolved GitLab file path
	OldYAML     string `json:"oldYaml"`     // content at submit time (for diff)
	NewYAML     string `json:"newYaml"`     // proposed content
	Reason      string `json:"reason"`      // change description
	Status      string `json:"status"`      // pending / approved / rejected
	Requester   string `json:"requester"`
	Reviewer    string `json:"reviewer,omitempty"`
	ReviewNote  string `json:"reviewNote,omitempty"`
	CommitError string `json:"commitError,omitempty"`
	CreatedAt   string `json:"createdAt"`
	ReviewedAt  string `json:"reviewedAt,omitempty"`
}

// Change request statuses.
const (
	ChangeRequestPending  = "pending"
	ChangeRequestApproved = "approved"
	ChangeRequestRejected = "rejected"
)

// ErrNotFound is returned when an entity is not found.
type ErrNotFound struct {
	Entity string
	Name   string
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("%s %q not found", e.Entity, e.Name)
}

// ErrDuplicate is returned when a duplicate name is detected.
type ErrDuplicate struct {
	Entity string
	Name   string
}

func (e *ErrDuplicate) Error() string {
	return fmt.Sprintf("%s %q already exists", e.Entity, e.Name)
}

// ErrReferenced is returned when trying to delete a referenced entity.
type ErrReferenced struct {
	Entity    string
	Name      string
	TaskNames []string
}

func (e *ErrReferenced) Error() string {
	return fmt.Sprintf("cannot delete %s %q: referenced by tasks %v", e.Entity, e.Name, e.TaskNames)
}

// NotifyChannel represents a notification channel (e.g. Feishu webhook).
type NotifyChannel struct {
	Name       string `json:"name"`
	Type       string `json:"type"`       // "feishu"
	WebhookURL string `json:"webhookUrl"`
}
