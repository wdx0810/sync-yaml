package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/configmap-sync/configmap-sync/internal/drift"
	"github.com/configmap-sync/configmap-sync/internal/engine"
	"github.com/configmap-sync/configmap-sync/internal/history"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

// Server is the HTTP API server.
type Server struct {
	engine      engine.Engine
	drift       drift.Detector
	history     history.Store
	sourceStore store.SourceStore
	targetStore store.TargetStore
	taskStore   store.TaskStore
	taskManager engine.TaskManager
	connTester  store.ConnectionTester
	userStore   store.UserStore
	notifyStore store.NotifyStore
	router      *mux.Router
	logger      *slog.Logger
	storagePath string
}

// ServerConfig holds all dependencies for the API server.
type ServerConfig struct {
	Engine      engine.Engine
	Drift       drift.Detector
	History     history.Store
	SourceStore store.SourceStore
	TargetStore store.TargetStore
	TaskStore   store.TaskStore
	TaskManager engine.TaskManager
	ConnTester  store.ConnectionTester
	UserStore   store.UserStore
	NotifyStore store.NotifyStore
	StoragePath string
}

// NewServer creates a new API server.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		engine:      cfg.Engine,
		drift:       cfg.Drift,
		history:     cfg.History,
		sourceStore: cfg.SourceStore,
		targetStore: cfg.TargetStore,
		taskStore:   cfg.TaskStore,
		taskManager: cfg.TaskManager,
		connTester:  cfg.ConnTester,
		userStore:   cfg.UserStore,
		notifyStore: cfg.NotifyStore,
		router:      mux.NewRouter(),
		logger:      slog.Default().With("component", "api"),
		storagePath: cfg.StoragePath,
	}
	s.registerRoutes()
	return s
}

// Router returns the configured mux router.
func (s *Server) Router() *mux.Router {
	return s.router
}

func (s *Server) registerRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// Recover from panics so a single bad sync doesn't bring the whole server
	// down or drop the client connection (which manifests as "network error").
	api.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					s.logger.Error("panic recovered", "path", r.URL.Path, "panic", rec)
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("服务端异常: %v", rec))
				}
			}()
			next.ServeHTTP(w, r)
		})
	})

	// Auth endpoint (public).
	api.HandleFunc("/auth/login", s.handleLogin).Methods("POST")
	api.HandleFunc("/auth/check", s.handleCheckAuth).Methods("GET")
	api.HandleFunc("/auth/change-password", s.handleChangePassword).Methods("POST")
	api.HandleFunc("/auth/mfa/verify", s.handleMFAVerify).Methods("POST")

	// Auth middleware for all other routes.
	api.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/auth/login") || strings.HasSuffix(r.URL.Path, "/auth/check") || strings.HasSuffix(r.URL.Path, "/auth/mfa/verify") {
				next.ServeHTTP(w, r)
				return
			}
			token := extractToken(r)
			if token == "" {
				writeError(w, http.StatusUnauthorized, "未登录")
				return
			}
			username, err := validateToken(token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "登录过期")
				return
			}
			r.Header.Set("X-Username", username)
			next.ServeHTTP(w, r)
		})
	})

	// User management (admin only).
	api.HandleFunc("/users", s.requireAdmin(s.handleListUsers)).Methods("GET")
	api.HandleFunc("/users", s.requireAdmin(s.handleCreateUser)).Methods("POST")
	api.HandleFunc("/users/me", s.handleCurrentUser).Methods("GET")
	api.HandleFunc("/users/{username}", s.requireAdmin(s.handleGetUserByUsername)).Methods("GET")
	api.HandleFunc("/users/{username}", s.requireAdmin(s.handleUpdateUserWrapper)).Methods("PUT")
	api.HandleFunc("/users/{username}", s.requireAdmin(s.handleDeleteUserWrapper)).Methods("DELETE")
	api.HandleFunc("/users/{username}/permissions", s.requireAdmin(s.handleGetUserPermissionsWrapper)).Methods("GET")
	api.HandleFunc("/users/{username}/permissions/{taskId}", s.requireAdmin(s.handleSetTaskPermissionWrapper)).Methods("PUT")
	api.HandleFunc("/users/{username}/permissions/{taskId}", s.requireAdmin(s.handleRemoveTaskPermissionWrapper)).Methods("DELETE")
	api.HandleFunc("/users/{username}/project-permissions/{project}", s.requireAdmin(s.handleSetProjectPermissionWrapper)).Methods("PUT")
	api.HandleFunc("/users/{username}/project-permissions/{project}", s.requireAdmin(s.handleRemoveProjectPermissionWrapper)).Methods("DELETE")
	api.HandleFunc("/users/{username}/mfa-reset", s.requireAdmin(s.handleResetUserMFAWrapper)).Methods("POST")
	api.HandleFunc("/users/{username}/mfa-enabled", s.requireAdmin(s.handleSetUserMFAEnabledWrapper)).Methods("PUT")

	// Existing endpoints.
	api.HandleFunc("/configmaps", s.listConfigMaps).Methods("GET")
	api.HandleFunc("/configmaps/{namespace}/{name}", s.getConfigMapDetail).Methods("GET")
	api.HandleFunc("/forward-sync", s.forwardSync).Methods("POST")
	api.HandleFunc("/forward-sync/{namespace}/{name}", s.forwardSyncOne).Methods("POST")
	api.HandleFunc("/reverse-sync/{namespace}/{name}", s.reverseSync).Methods("POST")
	api.HandleFunc("/drift-alerts", s.getDriftAlerts).Methods("GET")
	api.HandleFunc("/drift-alerts/{id}/dismiss", s.dismissAlert).Methods("POST")
	api.HandleFunc("/history", s.getHistory).Methods("GET")
	api.HandleFunc("/history/{id}", s.getHistoryRecord).Methods("GET")
	api.HandleFunc("/check-gitlab", s.checkGitLab).Methods("POST")
	api.HandleFunc("/status", s.getStatus).Methods("GET")

	// Sources endpoints.
	api.HandleFunc("/sources", s.listSources).Methods("GET")
	api.HandleFunc("/sources", s.createSource).Methods("POST")
	api.HandleFunc("/sources/{name}", s.updateSource).Methods("PUT")
	api.HandleFunc("/sources/{name}", s.deleteSource).Methods("DELETE")
	api.HandleFunc("/sources/{name}/test", s.testSource).Methods("POST")

	// Targets endpoints.
	api.HandleFunc("/targets", s.listTargets).Methods("GET")
	api.HandleFunc("/targets", s.createTarget).Methods("POST")
	api.HandleFunc("/targets/{name}", s.updateTarget).Methods("PUT")
	api.HandleFunc("/targets/{name}", s.deleteTarget).Methods("DELETE")
	api.HandleFunc("/targets/{name}/test", s.testTarget).Methods("POST")

	// Notify channels endpoints.
	api.HandleFunc("/notify-channels", s.listNotifyChannels).Methods("GET")
	api.HandleFunc("/notify-channels", s.createNotifyChannel).Methods("POST")
	api.HandleFunc("/notify-channels/{name}", s.updateNotifyChannel).Methods("PUT")
	api.HandleFunc("/notify-channels/{name}", s.deleteNotifyChannel).Methods("DELETE")

	// MFA management endpoints (protected).
	api.HandleFunc("/auth/mfa/status", s.handleMFAStatus).Methods("GET")
	api.HandleFunc("/auth/mfa/setup", s.handleMFASetup).Methods("POST")
	api.HandleFunc("/auth/mfa/enable", s.handleMFAEnable).Methods("POST")
	api.HandleFunc("/auth/mfa/disable", s.handleMFADisable).Methods("POST")

	// Tasks endpoints.
	api.HandleFunc("/tasks", s.listTasks).Methods("GET")
	api.HandleFunc("/tasks", s.createTask).Methods("POST")
	api.HandleFunc("/tasks/{id}", s.updateTask).Methods("PUT")
	api.HandleFunc("/tasks/{id}", s.deleteTask).Methods("DELETE")
	api.HandleFunc("/tasks/{id}/start", s.startTask).Methods("POST")
	api.HandleFunc("/tasks/{id}/pause", s.pauseTask).Methods("POST")
	api.HandleFunc("/tasks/{id}/sync", s.triggerSync).Methods("POST")
	api.HandleFunc("/tasks/{id}/preview", s.previewSync).Methods("POST")
	api.HandleFunc("/tasks/{id}/apply", s.applyChanges).Methods("POST")

	// Dashboard endpoint.
	api.HandleFunc("/dashboard", s.getDashboardData).Methods("GET")
}

// requireAdmin middleware checks if the current user is an admin.
func (s *Server) requireAdmin(next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-Username")
		user, err := s.userStore.GetUser(username)
		if err != nil || user.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r)
	})
}

// handleGetUserByUsername is a wrapper to extract username from URL.
func (s *Server) handleGetUserByUsername(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	s.handleGetUser(w, r, username)
}

// handleUpdateUserWrapper is a wrapper to extract username from URL.
func (s *Server) handleUpdateUserWrapper(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	s.handleUpdateUser(w, r, username)
}

// handleDeleteUserWrapper is a wrapper to extract username from URL.
func (s *Server) handleDeleteUserWrapper(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	s.handleDeleteUser(w, r, username)
}

// handleGetUserPermissionsWrapper is a wrapper to extract username from URL.
func (s *Server) handleGetUserPermissionsWrapper(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	s.handleGetUserPermissions(w, r, username)
}

// handleSetTaskPermissionWrapper is a wrapper to extract username and taskId from URL.
func (s *Server) handleSetTaskPermissionWrapper(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	s.handleSetTaskPermission(w, r, vars["username"], vars["taskId"])
}

// handleRemoveTaskPermissionWrapper is a wrapper to extract username and taskId from URL.
func (s *Server) handleRemoveTaskPermissionWrapper(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	s.handleRemoveTaskPermission(w, r, vars["username"], vars["taskId"])
}

// handleSetProjectPermissionWrapper extracts username and project from URL.
func (s *Server) handleSetProjectPermissionWrapper(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	s.handleSetProjectPermission(w, r, vars["username"], vars["project"])
}

// handleRemoveProjectPermissionWrapper extracts username and project from URL.
func (s *Server) handleRemoveProjectPermissionWrapper(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	s.handleRemoveProjectPermission(w, r, vars["username"], vars["project"])
}

// handleResetUserMFAWrapper extracts username from URL.
func (s *Server) handleResetUserMFAWrapper(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	s.handleResetUserMFA(w, r, username)
}

// handleSetUserMFAEnabledWrapper extracts username from URL.
func (s *Server) handleSetUserMFAEnabledWrapper(w http.ResponseWriter, r *http.Request) {
	username := mux.Vars(r)["username"]
	s.handleSetUserMFAEnabled(w, r, username)
}

// getStatus handles GET /api/v1/status
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ready":            s.engine != nil,
		"gitlabConfigured": s.engine != nil,
		"k8sConfigured":    s.engine != nil,
	})
}

func notConfigured(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "GitLab/K8s 连接尚未配置，请先在设置页面配置连接信息")
}

// listConfigMaps handles GET /api/v1/configmaps
func (s *Server) listConfigMaps(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	statuses, err := s.engine.GetManagedConfigMaps(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statuses)
}

func (s *Server) getConfigMapDetail(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		notConfigured(w)
		return
	}
	vars := mux.Vars(r)
	detail, err := s.engine.GetConfigMapDetail(r.Context(), vars["namespace"], vars["name"])
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) forwardSync(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		notConfigured(w)
		return
	}
	result, err := s.engine.ForwardSync(r.Context(), engine.ForwardSyncOptions{FullSync: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) forwardSyncOne(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		notConfigured(w)
		return
	}
	vars := mux.Vars(r)
	result, err := s.engine.ForwardSyncOne(r.Context(), vars["namespace"], vars["name"])
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) reverseSync(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		notConfigured(w)
		return
	}
	vars := mux.Vars(r)
	result, err := s.engine.ReverseSync(r.Context(), vars["namespace"], vars["name"])
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getDriftAlerts(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	writeJSON(w, http.StatusOK, s.drift.GetAlerts())
}

func (s *Server) dismissAlert(w http.ResponseWriter, r *http.Request) {
	if s.drift == nil {
		notConfigured(w)
		return
	}
	id := mux.Vars(r)["id"]
	if err := s.drift.DismissAlert(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	filter := history.QueryFilter{
		Name:      r.URL.Query().Get("name"),
		Namespace: r.URL.Query().Get("namespace"),
		Direction: r.URL.Query().Get("direction"),
	}
	if since := r.URL.Query().Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'since' parameter")
			return
		}
		filter.Since = &t
	}
	if until := r.URL.Query().Get("until"); until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'until' parameter")
			return
		}
		filter.Until = &t
	}
	// Pagination.
	page := 1
	pageSize := 50
	if p := r.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if ps := r.URL.Query().Get("pageSize"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}
	filter.Page = page
	filter.PageSize = pageSize

	result, err := s.history.Query(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getHistoryRecord(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	record, err := s.history.GetRecord(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) checkGitLab(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		notConfigured(w)
		return
	}
	result, err := s.engine.CheckGitLabChanges(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]interface{}{
		"error":   http.StatusText(status),
		"message": message,
		"status":  status,
	})
}

// Persistence helpers (kept for backward compat).
func (s *Server) connectionsFilePath() string {
	return filepath.Join(s.storagePath, "connections.json")
}

func (s *Server) loadConnections() {
	// Legacy: no longer used, connections are now in source/target stores.
}

func (s *Server) saveConnections() {
	// Legacy: no longer used.
}

// Unused but kept to avoid breaking references.
var _ = fmt.Sprintf
var _ = os.ReadFile
