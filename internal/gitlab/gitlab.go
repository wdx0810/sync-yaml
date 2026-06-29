package gitlab

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// FileDiff represents a file change between two commits.
type FileDiff struct {
	Path       string `json:"path"`
	OldPath    string `json:"oldPath,omitempty"`
	NewFile    bool   `json:"newFile"`
	DeletedFile bool  `json:"deletedFile"`
	Diff       string `json:"diff"`
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
	// CompareByTime returns diffs between two time points on the branch.
	CompareByTime(ctx context.Context, since, until string, path string) ([]FileDiff, error)
	// ListCommits returns recent commits for the branch/path.
	ListCommits(ctx context.Context, path string, limit int) ([]CommitInfo, error)
	// CompareCommits returns diffs between two commit SHAs.
	CompareCommits(ctx context.Context, from, to string, path string) ([]FileDiff, error)
	// ListFiles lists all YAML file paths under the given path (no content fetch).
	ListFiles(ctx context.Context, path string) ([]string, error)
	// GetFile fetches the raw content of a single file.
	GetFile(ctx context.Context, path string) ([]byte, error)
}

// CommitInfo represents a GitLab commit summary.
type CommitInfo struct {
	ID        string `json:"id"`
	ShortID   string `json:"shortId"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	CreatedAt string `json:"createdAt"`
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
// Uses concurrent workers to fetch file contents in parallel.
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

	// Phase 1: List all YAML file paths (fast, paginated).
	var filePaths []string
	for {
		nodes, resp, err := c.gl.Repositories.ListTree(c.projectID, opts, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, c.handleError("FetchFiles", err)
		}
		for _, node := range nodes {
			if node.Type != "blob" || !isYAMLFile(node.Path) {
				continue
			}
			filePaths = append(filePaths, node.Path)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	// Phase 2: Fetch file contents concurrently (20 workers).
	type fetchResult struct {
		idx     int
		content []byte
		path    string
		err     error
	}

	const fetchWorkers = 20
	workers := fetchWorkers
	if len(filePaths) < workers {
		workers = len(filePaths)
	}
	if workers == 0 {
		c.status = StatusConnected
		return nil, nil
	}

	jobs := make(chan int, len(filePaths))
	results := make(chan fetchResult, len(filePaths))

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				fp := filePaths[idx]
				content, err := c.getFileContent(ctx, fp)
				results <- fetchResult{idx: idx, content: content, path: fp, err: err}
			}
		}()
	}

	for i := range filePaths {
		jobs <- i
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results in order.
	filesMap := make(map[int]FileContent)
	for r := range results {
		if r.err != nil {
			c.logger.Warn("failed to fetch file content", "path", r.path, "error", r.err)
			continue
		}
		filesMap[r.idx] = FileContent{Path: r.path, Content: r.content}
	}

	files := make([]FileContent, 0, len(filesMap))
	for i := 0; i < len(filePaths); i++ {
		if f, ok := filesMap[i]; ok {
			files = append(files, f)
		}
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

// ListFiles lists all YAML file paths under the given path (no content fetch).
func (c *clientImpl) ListFiles(ctx context.Context, path string) ([]string, error) {
	if path == "" {
		path = c.basePath
	}
	path = strings.TrimPrefix(path, "/")

	opts := &gogitlab.ListTreeOptions{
		Path:      gogitlab.Ptr(path),
		Ref:       gogitlab.Ptr(c.branch),
		Recursive: gogitlab.Ptr(true),
		ListOptions: gogitlab.ListOptions{
			PerPage: 100,
		},
	}

	var filePaths []string
	for {
		nodes, resp, err := c.gl.Repositories.ListTree(c.projectID, opts, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, c.handleError("ListFiles", err)
		}
		for _, node := range nodes {
			if node.Type != "blob" || !isYAMLFile(node.Path) {
				continue
			}
			filePaths = append(filePaths, node.Path)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	c.status = StatusConnected
	return filePaths, nil
}

// GetFile fetches the raw content of a single file.
func (c *clientImpl) GetFile(ctx context.Context, path string) ([]byte, error) {
	path = strings.TrimPrefix(path, "/")
	content, err := c.getFileContent(ctx, path)
	if err != nil {
		return nil, c.handleError("GetFile", err)
	}
	c.status = StatusConnected
	return content, nil
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

// CompareByTime returns diffs between two time points on the branch.
// Uses GitLab Commits API to find commits at those times, then Compare.
func (c *clientImpl) CompareByTime(ctx context.Context, since, until string, path string) ([]FileDiff, error) {
	// GitLab API path must not have leading slash.
	path = strings.TrimPrefix(path, "/")

	// List commits in the time range to get the boundary SHAs.
	sinceTime, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return nil, fmt.Errorf("invalid since time: %w", err)
	}
	untilTime, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return nil, fmt.Errorf("invalid until time: %w", err)
	}

	// List all commits in the time range.
	opts := &gogitlab.ListCommitsOptions{
		RefName: gogitlab.Ptr(c.branch),
		Since:   gogitlab.Ptr(sinceTime),
		Until:   gogitlab.Ptr(untilTime),
		ListOptions: gogitlab.ListOptions{PerPage: 100},
	}
	if path != "" {
		opts.Path = gogitlab.Ptr(path)
	}

	commitsInRange, _, err := c.gl.Commits.ListCommits(c.projectID, opts, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, c.handleError("CompareByTime.ListCommits", err)
	}

	if len(commitsInRange) == 0 {
		return nil, nil // no commits in this period
	}

	// headSHA = the latest commit in range (first in list, since sorted by date desc).
	headSHA := commitsInRange[0].ID
	// baseSHA = the parent of the oldest commit in range.
	oldestSHA := commitsInRange[len(commitsInRange)-1].ID

	// Get the parent of the oldest commit by listing one commit before 'since'.
	beforeOpts := &gogitlab.ListCommitsOptions{
		RefName: gogitlab.Ptr(c.branch),
		Until:   gogitlab.Ptr(sinceTime),
		ListOptions: gogitlab.ListOptions{PerPage: 1},
	}
	if path != "" {
		beforeOpts.Path = gogitlab.Ptr(path)
	}
	beforeCommits, _, _ := c.gl.Commits.ListCommits(c.projectID, beforeOpts, gogitlab.WithContext(ctx))
	baseSHA := oldestSHA + "~1" // fallback: parent of oldest
	if len(beforeCommits) > 0 {
		baseSHA = beforeCommits[0].ID
	}

	// Compare base to head.
	compare, _, err := c.gl.Repositories.Compare(c.projectID, &gogitlab.CompareOptions{
		From: gogitlab.Ptr(baseSHA),
		To:   gogitlab.Ptr(headSHA),
	}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, c.handleError("CompareByTime", err)
	}

	var diffs []FileDiff
	for _, d := range compare.Diffs {
		p := d.NewPath
		if p == "" {
			p = d.OldPath
		}
		// Filter by path prefix if specified.
		if path != "" && !strings.HasPrefix(p, path) {
			continue
		}
		diffs = append(diffs, FileDiff{
			Path:        p,
			OldPath:     d.OldPath,
			NewFile:     d.NewFile,
			DeletedFile: d.DeletedFile,
			Diff:        d.Diff,
		})
	}
	return diffs, nil
}

// ListCommits returns recent commits for the branch/path.
func (c *clientImpl) ListCommits(ctx context.Context, path string, limit int) ([]CommitInfo, error) {
	if limit <= 0 {
		limit = 50
	}
	// GitLab API path must not have leading slash.
	path = strings.TrimPrefix(path, "/")

	opts := &gogitlab.ListCommitsOptions{
		RefName:     gogitlab.Ptr(c.branch),
		ListOptions: gogitlab.ListOptions{PerPage: limit},
	}
	if path != "" {
		opts.Path = gogitlab.Ptr(path)
	}

	commits, _, err := c.gl.Commits.ListCommits(c.projectID, opts, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, c.handleError("ListCommits", err)
	}

	var result []CommitInfo
	for _, cm := range commits {
		result = append(result, CommitInfo{
			ID:        cm.ID,
			ShortID:   cm.ShortID,
			Title:     cm.Title,
			Author:    cm.AuthorName,
			CreatedAt: cm.CreatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

// CompareCommits returns diffs between two commit SHAs.
func (c *clientImpl) CompareCommits(ctx context.Context, from, to string, path string) ([]FileDiff, error) {
	// GitLab API path must not have leading slash.
	path = strings.TrimPrefix(path, "/")

	compare, _, err := c.gl.Repositories.Compare(c.projectID, &gogitlab.CompareOptions{
		From: gogitlab.Ptr(from),
		To:   gogitlab.Ptr(to),
	}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, c.handleError("CompareCommits", err)
	}

	var diffs []FileDiff
	for _, d := range compare.Diffs {
		p := d.NewPath
		if p == "" {
			p = d.OldPath
		}
		if path != "" && !strings.HasPrefix(p, path) {
			continue
		}
		diffs = append(diffs, FileDiff{
			Path:        p,
			OldPath:     d.OldPath,
			NewFile:     d.NewFile,
			DeletedFile: d.DeletedFile,
			Diff:        d.Diff,
		})
	}
	return diffs, nil
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
