package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/configmap-sync/configmap-sync/internal/store"
)

// handleListUsers handles GET /api/v1/users
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.userStore.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// handleGetUser handles GET /api/v1/users/{username}
func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request, username string) {
	user, err := s.userStore.GetUser(username)
	if err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// handleCreateUser handles POST /api/v1/users
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		Role     store.Role `json:"role"`
		Enabled  bool     `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "用户名和密码不能为空")
		return
	}

	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}

	if req.Role == "" {
		req.Role = store.RoleUser
	}

	user := &store.User{
		Username: req.Username,
		Password: req.Password,
		Role:     req.Role,
		Enabled:  req.Enabled,
	}

	if err := s.userStore.CreateUser(user); err != nil {
		if _, ok := err.(*store.ErrDuplicate); ok {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return without password
	user.Password = ""
	writeJSON(w, http.StatusCreated, user)
}

// handleUpdateUser handles PUT /api/v1/users/{username}
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, username string) {
	var req struct {
		Password string     `json:"password"`
		Role     store.Role `json:"role"`
		Enabled  *bool      `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// Get existing user
	existing, err := s.userStore.GetUser(username)
	if err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	user := &store.User{
		Username: username,
		Password: req.Password,
		Role:     req.Role,
		Enabled:  existing.Enabled,
	}
	if req.Enabled != nil {
		user.Enabled = *req.Enabled
	}
	if req.Role == "" {
		user.Role = existing.Role
	}

	if err := s.userStore.UpdateUser(username, user); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	user.Password = ""
	writeJSON(w, http.StatusOK, user)
}

// handleDeleteUser handles DELETE /api/v1/users/{username}
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, username string) {
	// Prevent self-deletion
	currentUser := r.Header.Get("X-Username")
	if currentUser == username {
		writeError(w, http.StatusBadRequest, "不能删除自己")
		return
	}

	if err := s.userStore.DeleteUser(username); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if _, ok := err.(*store.ErrReferenced); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleGetUserPermissions handles GET /api/v1/users/{username}/permissions
func (s *Server) handleGetUserPermissions(w http.ResponseWriter, r *http.Request, username string) {
	perms, err := s.userStore.GetUserPermissions(username)
	if err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, perms)
}

// handleSetTaskPermission handles PUT /api/v1/users/{username}/permissions/{taskId}
func (s *Server) handleSetTaskPermission(w http.ResponseWriter, r *http.Request, username string, taskID string) {
	var req struct {
		CanView bool `json:"canView"`
		CanSync bool `json:"canSync"`
		CanEdit bool `json:"canEdit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	perm := store.TaskPermission{
		TaskID:  taskID,
		CanView: req.CanView,
		CanSync: req.CanSync,
		CanEdit: req.CanEdit,
	}

	if err := s.userStore.SetTaskPermission(username, perm); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, perm)
}

// handleRemoveTaskPermission handles DELETE /api/v1/users/{username}/permissions/{taskId}
func (s *Server) handleRemoveTaskPermission(w http.ResponseWriter, r *http.Request, username string, taskID string) {
	if err := s.userStore.RemoveTaskPermission(username, taskID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleSetProjectPermission handles PUT /api/v1/users/{username}/project-permissions/{project}
func (s *Server) handleSetProjectPermission(w http.ResponseWriter, r *http.Request, username string, project string) {
	var req struct {
		CanView bool `json:"canView"`
		CanSync bool `json:"canSync"`
		CanEdit bool `json:"canEdit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	perm := store.ProjectPermission{
		Project: project,
		CanView: req.CanView,
		CanSync: req.CanSync,
		CanEdit: req.CanEdit,
	}
	if err := s.userStore.SetProjectPermission(username, perm); err != nil {
		if _, ok := err.(*store.ErrNotFound); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, perm)
}

// handleRemoveProjectPermission handles DELETE /api/v1/users/{username}/project-permissions/{project}
func (s *Server) handleRemoveProjectPermission(w http.ResponseWriter, r *http.Request, username string, project string) {
	if err := s.userStore.RemoveProjectPermission(username, project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleCurrentUser handles GET /api/v1/users/me
func (s *Server) handleCurrentUser(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Username")
	user, err := s.userStore.GetUser(username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// handleGenerateUserAPIToken generates an API token for a user.
func (s *Server) handleGenerateUserAPIToken(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	token, err := s.userStore.GenerateAPIToken(username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": username,
		"usage":    "Authorization: Bearer " + token,
	})
}

// handleDeleteUserAPIToken deletes the API token for a user.
func (s *Server) handleDeleteUserAPIToken(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	if err := s.userStore.DeleteAPIToken(username); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "API Token 已删除"})
}
