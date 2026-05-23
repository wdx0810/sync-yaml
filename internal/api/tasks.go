package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/configmap-sync/configmap-sync/internal/engine"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.taskStore.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Filter by user permissions: admin sees all; user sees only permitted tasks.
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
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var task store.SyncTask
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if task.Name == "" || task.SourceName == "" || task.TargetName == "" {
		writeError(w, http.StatusBadRequest, "name, sourceName, and targetName are required")
		return
	}
	if task.SyncMode == "" {
		task.SyncMode = "manual"
	}
	if task.SyncMode == "scheduled" && task.Interval < 30 {
		writeError(w, http.StatusBadRequest, "interval must be >= 30 seconds for scheduled mode")
		return
	}
	if err := s.taskStore.Create(&task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !s.checkTaskAccess(w, r, id, "edit") {
		return
	}
	var task store.SyncTask
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.taskStore.Update(id, &task); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// If task is running, restart it with new config.
	if s.taskManager != nil {
		if rs := s.taskManager.GetTaskStatus(id); rs != nil && rs.Status == "running" {
			_ = s.taskManager.StopTask(id)
			updated, _ := s.taskStore.Get(id)
			if updated != nil && updated.Status == "running" {
				_ = s.taskManager.StartTask(updated)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !s.checkTaskAccess(w, r, id, "edit") {
		return
	}
	// Stop if running.
	if s.taskManager != nil {
		_ = s.taskManager.StopTask(id)
	}
	if err := s.taskStore.Delete(id); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) startTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !s.checkTaskAccess(w, r, id, "edit") {
		return
	}
	task, err := s.taskStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if s.taskManager == nil {
		writeError(w, http.StatusServiceUnavailable, "task manager not initialized")
		return
	}
	if err := s.taskManager.StartTask(task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) pauseTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !s.checkTaskAccess(w, r, id, "edit") {
		return
	}
	if s.taskManager == nil {
		writeError(w, http.StatusServiceUnavailable, "task manager not initialized")
		return
	}
	if err := s.taskManager.StopTask(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (s *Server) triggerSync(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if s.taskManager == nil {
		writeError(w, http.StatusServiceUnavailable, "task manager not initialized")
		return
	}
	// Permission check: user must have sync permission for this task.
	if !s.checkTaskAccess(w, r, id, "sync") {
		return
	}
	info, err := s.taskManager.TriggerSync(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// previewSync returns the pending changes without applying them.
func (s *Server) previewSync(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if s.taskManager == nil {
		writeError(w, http.StatusServiceUnavailable, "task manager not initialized")
		return
	}
	if !s.checkTaskAccess(w, r, id, "sync") {
		return
	}
	result, err := s.taskManager.PreviewSync(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// applyChanges applies a user-approved list of pending changes.
func (s *Server) applyChanges(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if s.taskManager == nil {
		writeError(w, http.StatusServiceUnavailable, "task manager not initialized")
		return
	}
	if !s.checkTaskAccess(w, r, id, "sync") {
		return
	}
	var req struct {
		Changes []engine.PendingChange `json:"changes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	info, err := s.taskManager.ApplyChanges(id, req.Changes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// checkTaskAccess returns true if the current user is allowed to perform the given action on the task.
// Writes a 403 error response and returns false on denial.
func (s *Server) checkTaskAccess(w http.ResponseWriter, r *http.Request, taskID, action string) bool {
	if s.userStore == nil {
		return true
	}
	username := r.Header.Get("X-Username")
	if username == "" {
		writeError(w, http.StatusUnauthorized, "未登录")
		return false
	}
	// Resolve the task's project for project-level permission lookup.
	project := ""
	if s.taskStore != nil {
		if t, err := s.taskStore.Get(taskID); err == nil {
			project = t.Project
		}
	}
	allowed, err := s.userStore.CanAccessTask(username, taskID, project, action)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "没有该任务的权限")
		return false
	}
	return true
}
