package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/configmap-sync/configmap-sync/internal/feishu"
	"github.com/configmap-sync/configmap-sync/internal/store"
)

// handleGetFeishuConfig returns the Feishu integration config (secret masked). Admin only.
func (s *Server) handleGetFeishuConfig(w http.ResponseWriter, r *http.Request) {
	if s.feishuStore == nil {
		writeJSON(w, http.StatusOK, &store.FeishuConfig{})
		return
	}
	cfg, err := s.feishuStore.Get()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleSaveFeishuConfig saves the Feishu integration config. Admin only.
func (s *Server) handleSaveFeishuConfig(w http.ResponseWriter, r *http.Request) {
	if s.feishuStore == nil {
		writeError(w, http.StatusServiceUnavailable, "feishu store 未初始化")
		return
	}
	var cfg store.FeishuConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := s.feishuStore.Save(&cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleFeishuStatus reports whether Feishu login is enabled (public, for login page).
func (s *Server) handleFeishuStatus(w http.ResponseWriter, r *http.Request) {
	enabled := false
	if s.feishuStore != nil {
		if cfg, err := s.feishuStore.GetWithSecret(); err == nil {
			enabled = cfg.Enabled && cfg.AppID != "" && cfg.AppSecret != ""
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

// handleFeishuLogin redirects the browser to Feishu's authorization page (public).
func (s *Server) handleFeishuLogin(w http.ResponseWriter, r *http.Request) {
	if s.feishuStore == nil {
		writeError(w, http.StatusServiceUnavailable, "飞书未配置")
		return
	}
	cfg, err := s.feishuStore.GetWithSecret()
	if err != nil || !cfg.Enabled || cfg.AppID == "" || cfg.AppSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "飞书登录未启用或未配置完整")
		return
	}
	fc := feishu.NewClient(cfg.AppID, cfg.AppSecret)
	// state carries nothing sensitive here; used only to satisfy the flow.
	state := "yamlsync"
	http.Redirect(w, r, fc.AuthURL(cfg.RedirectURI, state), http.StatusFound)
}

// handleFeishuCallback handles Feishu's redirect with ?code=...  (public).
// It exchanges the code for user info, upserts the user (Feishu-only account),
// issues a session token, and redirects to the SPA with the token.
func (s *Server) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	if s.feishuStore == nil {
		writeError(w, http.StatusServiceUnavailable, "飞书未配置")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "缺少 code")
		return
	}
	cfg, err := s.feishuStore.GetWithSecret()
	if err != nil || !cfg.Enabled {
		writeError(w, http.StatusServiceUnavailable, "飞书登录未启用")
		return
	}
	fc := feishu.NewClient(cfg.AppID, cfg.AppSecret)
	info, err := fc.ExchangeCode(r.Context(), code)
	if err != nil {
		s.redirectLoginError(w, r, "飞书授权失败: "+err.Error())
		return
	}
	if info.Email == "" && info.UserID == "" && info.OpenID == "" {
		s.redirectLoginError(w, r, "未获取到飞书用户信息")
		return
	}

	// Upsert: bind to existing account (by email) or create a no-permission user.
	user, err := s.userStore.UpsertFeishuUser(info.Email, info.Name, info.UserID, info.OpenID)
	if err != nil {
		s.redirectLoginError(w, r, "创建/绑定用户失败: "+err.Error())
		return
	}
	if !user.Enabled {
		s.redirectLoginError(w, r, "该账号已被禁用")
		return
	}

	// Issue a session token (Feishu login skips MFA).
	expiresAt := time.Now().Add(24 * time.Hour).Unix()
	token := generateToken(user.Username, expiresAt)

	// Redirect back to SPA with token + username so the frontend can store them.
	q := url.Values{}
	q.Set("token", token)
	q.Set("username", user.Username)
	http.Redirect(w, r, "/login/feishu-callback?"+q.Encode(), http.StatusFound)
}

func (s *Server) redirectLoginError(w http.ResponseWriter, r *http.Request, msg string) {
	q := url.Values{}
	q.Set("error", msg)
	http.Redirect(w, r, "/login/feishu-callback?"+q.Encode(), http.StatusFound)
}
