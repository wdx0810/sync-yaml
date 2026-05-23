package gitlab

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	gogitlab "github.com/xanzy/go-gitlab"
)

// ChangeType represents the type of file change.
type ChangeType string

const (
	ChangeAdded    ChangeType = "added"
	ChangeModified ChangeType = "modified"
	ChangeDeleted  ChangeType = "deleted"
)

// FileContent represents a file fetched from GitLab.
type FileContent struct {
	Path    string
	Content []byte
}

// FileChange represents a detected file change in GitLab.
type FileChange struct {
	Path       string     `json:"path"`
	ChangeType ChangeType `json:"changeType"`
	Content    []byte     `json:"-"`
}

// FileCommitAction represents a single file action within a multi-file commit.
type FileCommitAction struct {
	Path    string
	Content []byte
}

// Client defines the interface for GitLab interactions.
type Client interface {
	// FetchFiles fetches all YAML files under the given path.
	FetchFiles(ctx context.Context, path string) ([]FileContent, error)
	// CheckChanges detects file changes since the given commit SHA.
	CheckChanges(ctx context.Context, since string) ([]FileChange, error)
	// CommitFile commits a single file change to GitLab.
	CommitFile(ctx context.Context, path string, content []byte, message string) error
	// CommitFiles commits multiple files in a single atomic commit.
	CommitFiles(ctx context.Context, files []FileCommitAction, message string) error
}

// ConnectionStatus represents the GitLab connection state.
type ConnectionStatus string

const (
	StatusConnected    ConnectionStatus = "Connected"
	StatusDisconnected ConnectionStatus = "Disconnected"
)

// clientImpl is the concrete implementation of Client using go-gitlab.
type clientImpl struct {
	gl        *gogitlab.Client
	projectID int
	branch    string
	basePath  string
	status    ConnectionStatus
	logger    *slog.Logger
}

// NewClient creates a new GitLab client.
func NewClient(url, token string, projectID int, branch, basePath string) (Client, error) {
	gl, err := gogitlab.NewClient(token, gogitlab.WithBaseURL(url))
	if err != nil {
		return nil, fmt.Errorf("failed to create gitlab client: %w", err)
	}
	logger := slog.Default().With("component", "gitlab")
	return &clientImpl{
		gl:        gl,
		projectID: projectID,
		branch:    branch,
		basePath:  basePath,
		status:    StatusConnected,
		logger:    logger,
	}, nil
}

// isYAMLFile checks if a file path has a .yaml or .yml extension.
func isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// FetchFiles fetches all YAML files under the given path from GitLab.
func (c *clientImpl) FetchFiles(ctx context.Context, path string) ([]FileContent, error) {
	if path == "" {
		path = c.basePath
	}
	// GitLab API path must not have leading slash.
	path = strings.TrimPrefix(path, "/")

	opts := &gogitlab.ListTreeOptions{
		Path:      gogitlab.Ptr(path),
		Ref:       gogitlab.Ptr(c.branch),
		Recursive: gogitlab.Ptr(true),
		ListOptions: gogitlab.ListOptions{
			PerPage: 100,
		},
	}

	var files []FileContent
	for {
		nodes, resp, err := c.gl.Repositories.ListTree(c.projectID, opts, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, c.handleError("FetchFiles", err)
		}

		for _, node := range nodes {
			if node.Type != "blob" || !isYAMLFile(node.Path) {
				continue
			}
			content, err := c.getFileContent(ctx, node.Path)
			if err != nil {
				c.logger.Warn("failed to fetch file content", "path", node.Path, "error", err)
				continue
			}
			files = append(files, FileContent{
				Path:    node.Path,
				Content: content,
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	c.status = StatusConnected
	return files, nil
}

// getFileContent fetches the raw content of a single file.
func (c *clientImpl) getFileContent(ctx context.Context, path string) ([]byte, error) {
	raw, _, err := c.gl.RepositoryFiles.GetRawFile(
		c.projectID,
		path,
		&gogitlab.GetRawFileOptions{Ref: gogitlab.Ptr(c.branch)},
		gogitlab.WithContext(ctx),
	)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// CheckChanges detects file changes since the given commit SHA.
func (c *clientImpl) CheckChanges(ctx context.Context, since string) ([]FileChange, error) {
	opts := &gogitlab.CompareOptions{
		From: gogitlab.Ptr(since),
		To:   gogitlab.Ptr(c.branch),
	}

	compare, _, err := c.gl.Repositories.Compare(c.projectID, opts, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, c.handleError("CheckChanges", err)
	}

	var changes []FileChange
	for _, diff := range compare.Diffs {
		path := diff.NewPath
		if path == "" {
			path = diff.OldPath
		}

		if !isYAMLFile(path) {
			continue
		}

		change := FileChange{Path: path}

		switch {
		case diff.NewFile:
			change.ChangeType = ChangeAdded
		case diff.DeletedFile:
			change.ChangeType = ChangeDeleted
		default:
			change.ChangeType = ChangeModified
		}

		// Fetch content for non-deleted files.
		if change.ChangeType != ChangeDeleted {
			content, err := c.getFileContent(ctx, path)
			if err != nil {
				c.logger.Warn("failed to fetch changed file content", "path", path, "error", err)
				continue
			}
			change.Content = content
		}

		changes = append(changes, change)
	}

	c.status = StatusConnected
	return changes, nil
}

// CommitFile commits a file change to GitLab on the target branch.
// If the file doesn't exist, it creates it; otherwise it updates it.
func (c *clientImpl) CommitFile(ctx context.Context, path string, content []byte, message string) error {
	return c.CommitFiles(ctx, []FileCommitAction{{Path: path, Content: content}}, message)
}

// CommitFiles commits multiple files to GitLab in a single atomic commit.
func (c *clientImpl) CommitFiles(ctx context.Context, files []FileCommitAction, message string) error {
	if len(files) == 0 {
		return nil
	}

	actions := make([]*gogitlab.CommitActionOptions, 0, len(files))
	for _, f := range files {
		// Determine create vs update by checking existence.
		action := gogitlab.FileCreate
		_, _, err := c.gl.RepositoryFiles.GetRawFile(
			c.projectID, f.Path,
			&gogitlab.GetRawFileOptions{Ref: gogitlab.Ptr(c.branch)},
			gogitlab.WithContext(ctx),
		)
		if err == nil {
			action = gogitlab.FileUpdate
		}
		actions = append(actions, &gogitlab.CommitActionOptions{
			Action:   gogitlab.Ptr(action),
			FilePath: gogitlab.Ptr(f.Path),
			Content:  gogitlab.Ptr(string(f.Content)),
		})
	}

	opts := &gogitlab.CreateCommitOptions{
		Branch:        gogitlab.Ptr(c.branch),
		CommitMessage: gogitlab.Ptr(message),
		Actions:       actions,
	}

	_, _, err := c.gl.Commits.CreateCommit(c.projectID, opts, gogitlab.WithContext(ctx))
	if err != nil {
		return c.handleError("CommitFiles", err)
	}

	c.status = StatusConnected
	return nil
}

// handleError classifies and logs GitLab API errors.
func (c *clientImpl) handleError(operation string, err error) error {
	// Check for network timeout errors.
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		c.status = StatusDisconnected
		c.logger.Error("network timeout", "operation", operation, "error", err)
		return fmt.Errorf("%s: network timeout: %w", operation, err)
	}

	// Check for HTTP error responses.
	var errResp *gogitlab.ErrorResponse
	if ok := isErrorResponse(err, &errResp); ok {
		statusCode := errResp.Response.StatusCode
		switch statusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			c.logger.Error("authentication failed", "operation", operation, "status", statusCode)
			return fmt.Errorf("%s: authentication failed (HTTP %d): %w", operation, statusCode, err)
		}
	}

	// Check for generic network errors (connection refused, DNS, etc.).
	if _, ok := err.(*net.OpError); ok {
		c.status = StatusDisconnected
		c.logger.Error("network error", "operation", operation, "error", err)
		return fmt.Errorf("%s: network error: %w", operation, err)
	}

	c.logger.Error("gitlab api error", "operation", operation, "error", err)
	return fmt.Errorf("%s: %w", operation, err)
}

// isErrorResponse checks if the error is a GitLab ErrorResponse.
func isErrorResponse(err error, target **gogitlab.ErrorResponse) bool {
	for err != nil {
		if e, ok := err.(*gogitlab.ErrorResponse); ok {
			*target = e
			return true
		}
		// Unwrap if possible.
		if unwrapper, ok := err.(interface{ Unwrap() error }); ok {
			err = unwrapper.Unwrap()
		} else {
			break
		}
	}
	return false
}

// Status returns the current connection status.
func (c *clientImpl) Status() ConnectionStatus {
	return c.status
}
