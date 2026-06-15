package api

import (
	"net/http"

	"github.com/configmap-sync/configmap-sync/internal/gitlab"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

// handleCompare handles GET /api/v1/compare?taskId=xxx&since=2026-06-01T00:00:00Z&until=2026-06-07T00:00:00Z
// Returns the GitLab file diffs between two time points for the task's source path.
func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("taskId")
	since := r.URL.Query().Get("since")
	until := r.URL.Query().Get("until")

	if taskID == "" || since == "" || until == "" {
		writeError(w, http.StatusBadRequest, "taskId, since, until 参数必填")
		return
	}

	task, err := s.taskStore.Get(taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}

	// Resolve source.
	var source *store.GitLabSource
	if task.Direction == "reverse" {
		source, err = s.sourceStore.Get(task.TargetName)
	} else {
		source, err = s.sourceStore.Get(task.SourceName)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "GitLab 数据源未找到")
		return
	}

	gc, err := gitlab.NewClient(source.URL, source.Token, source.ProjectID, source.Branch, source.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Use task-level source path if set.
	path := source.Path
	if task.SourcePath != "" {
		path = task.SourcePath
	}

	diffs, err := gc.CompareByTime(r.Context(), since, until, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if diffs == nil {
		diffs = []gitlab.FileDiff{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"taskId": taskID,
		"since":  since,
		"until":  until,
		"total":  len(diffs),
		"diffs":  diffs,
	})
}
