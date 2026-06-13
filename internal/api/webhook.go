package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gorilla/mux"
)

// handleWebhookSync handles POST /api/v1/hooks/sync/{id}?token=xxx
// This is a public endpoint that uses token-based auth instead of user login.
func (s *Server) handleWebhookSync(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	token := r.URL.Query().Get("token")

	if token == "" {
		// Also check Authorization header: Bearer <token>
		token = extractToken(r)
	}

	if token == "" {
		writeError(w, http.StatusUnauthorized, "缺少 token 参数")
		return
	}

	// Get task and verify token.
	task, err := s.taskStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}

	if task.WebhookToken == "" {
		writeError(w, http.StatusForbidden, "该任务未启用 Webhook，请先生成 Token")
		return
	}

	if task.WebhookToken != token {
		writeError(w, http.StatusUnauthorized, "Token 无效")
		return
	}

	// Trigger sync.
	if s.taskManager == nil {
		writeError(w, http.StatusServiceUnavailable, "task manager not initialized")
		return
	}

	info, err := s.taskManager.TriggerSync(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// handleGenerateWebhookToken handles POST /api/v1/tasks/{id}/webhook-token
// Generates a new random token for the task.
func (s *Server) handleGenerateWebhookToken(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	task, err := s.taskStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Generate 32-byte random token.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "生成 Token 失败")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	task.WebhookToken = token
	if err := s.taskStore.Update(id, task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"endpoint": "/api/v1/hooks/sync/" + id + "?token=" + token,
	})
}

// handleDeleteWebhookToken handles DELETE /api/v1/tasks/{id}/webhook-token
func (s *Server) handleDeleteWebhookToken(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	task, err := s.taskStore.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	task.WebhookToken = ""
	if err := s.taskStore.Update(id, task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "Token 已删除"})
}
