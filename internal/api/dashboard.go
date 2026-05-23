package api

import (
	"net/http"

	"github.com/configmap-sync/configmap-sync/internal/store"
)

type dashboardSummary struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Paused  int `json:"paused"`
	Error   int `json:"error"`
}

type dashboardResponse struct {
	Summary dashboardSummary `json:"summary"`
	Tasks   interface{}      `json:"tasks"`
}

func (s *Server) getDashboardData(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.taskStore.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter by user permissions (non-admin users only see tasks they can view).
	username := r.Header.Get("X-Username")
	if s.userStore != nil && username != "" {
		user, err := s.userStore.GetUser(username)
		if err == nil && user.Role != store.RoleAdmin {
			filtered := tasks[:0]
			for _, t := range tasks {
				if ok, _ := s.userStore.CanAccessTask(username, t.ID, t.Project, "view"); ok {
					filtered = append(filtered, t)
				}
			}
			tasks = filtered
		}
	}

	// Enrich with runtime status.
	if s.taskManager != nil {
		for i := range tasks {
			if rs := s.taskManager.GetTaskStatus(tasks[i].ID); rs != nil {
				tasks[i].Status = rs.Status
				if !rs.LastSyncTime.IsZero() {
					tasks[i].LastSyncTime = rs.LastSyncTime.Format("2006-01-02T15:04:05Z07:00")
				}
				tasks[i].LastSyncResult = rs.LastResult
				tasks[i].ErrorMessage = rs.ErrorMessage
			}
		}
	}

	summary := dashboardSummary{Total: len(tasks)}
	for _, t := range tasks {
		switch t.Status {
		case "running":
			summary.Running++
		case "paused":
			summary.Paused++
		case "error":
			summary.Error++
		}
	}

	writeJSON(w, http.StatusOK, dashboardResponse{
		Summary: summary,
		Tasks:   tasks,
	})
}
