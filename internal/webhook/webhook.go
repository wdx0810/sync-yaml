package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
)

// PushEvent represents a GitLab Push Event webhook payload.
type PushEvent struct {
	Ref     string   `json:"ref"`
	Commits []Commit `json:"commits"`
}

// Commit represents a single commit in a push event.
type Commit struct {
	ID       string   `json:"id"`
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}

// ChangedYAMLFiles returns all YAML file paths affected by this push event.
func (e *PushEvent) ChangedYAMLFiles() []string {
	seen := make(map[string]bool)
	var files []string
	for _, c := range e.Commits {
		for _, paths := range [][]string{c.Added, c.Modified, c.Removed} {
			for _, p := range paths {
				if isYAMLFile(p) && !seen[p] {
					seen[p] = true
					files = append(files, p)
				}
			}
		}
	}
	return files
}

// Receiver defines the interface for receiving GitLab webhooks.
type Receiver interface {
	// Handler returns an HTTP handler for the webhook endpoint.
	Handler() http.Handler
	// Events returns a channel that emits received push events.
	Events() <-chan PushEvent
}

// receiverImpl is the concrete implementation of Receiver.
type receiverImpl struct {
	secret string
	branch string
	events chan PushEvent
	logger *slog.Logger
}

// NewReceiver creates a new Webhook Receiver.
// secret is the GitLab webhook secret token for signature verification.
// branch is the target branch to filter events (e.g., "main").
func NewReceiver(secret, branch string) Receiver {
	return &receiverImpl{
		secret: secret,
		branch: branch,
		events: make(chan PushEvent, 100),
		logger: slog.Default().With("component", "webhook"),
	}
}

// Handler returns the HTTP handler for the webhook endpoint.
func (r *receiverImpl) Handler() http.Handler {
	return http.HandlerFunc(r.handleWebhook)
}

// Events returns the channel for receiving push events.
func (r *receiverImpl) Events() <-chan PushEvent {
	return r.events
}

// handleWebhook processes incoming GitLab webhook requests.
func (r *receiverImpl) handleWebhook(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify the secret token.
	token := req.Header.Get("X-Gitlab-Token")
	if !r.verifyToken(token) {
		r.logger.Error("webhook signature verification failed")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Check event type.
	eventType := req.Header.Get("X-Gitlab-Event")
	if eventType != "Push Hook" {
		r.logger.Debug("ignoring non-push event", "event", eventType)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Read and parse the body.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		r.logger.Error("failed to read webhook body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer req.Body.Close()

	var event PushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		r.logger.Error("failed to parse webhook payload", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Check if the event is for the target branch.
	expectedRef := fmt.Sprintf("refs/heads/%s", r.branch)
	if event.Ref != expectedRef {
		r.logger.Debug("ignoring push to non-target branch", "ref", event.Ref, "target", expectedRef)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Check if any YAML files were changed.
	yamlFiles := event.ChangedYAMLFiles()
	if len(yamlFiles) == 0 {
		r.logger.Debug("no YAML files changed in push event")
		w.WriteHeader(http.StatusOK)
		return
	}

	r.logger.Info("received push event with YAML changes", "files", yamlFiles)

	// Send event to channel (non-blocking).
	select {
	case r.events <- event:
	default:
		r.logger.Warn("webhook event channel full, dropping event")
	}

	w.WriteHeader(http.StatusOK)
}

// verifyToken checks if the provided token matches the configured secret.
func (r *receiverImpl) verifyToken(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(r.secret)) == 1
}

// isYAMLFile checks if a file path has a .yaml or .yml extension.
func isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}
