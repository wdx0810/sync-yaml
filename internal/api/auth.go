package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AuthConfig holds authentication settings (legacy; kept for backward compat).
type AuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Secret   string `json:"secret"`
}

// Default auth config.
var defaultAuth = AuthConfig{
	Username: "admin",
	Password: "admin123",
	Secret:   "yaml-sync-secret-key-2024",
}

// LoadAuth loads auth config from file, or returns default.
func LoadAuth(storagePath string) *AuthConfig {
	data, err := os.ReadFile(filepath.Join(storagePath, "auth.json"))
	if err != nil {
		return &defaultAuth
	}
	var auth AuthConfig
	if err := json.Unmarshal(data, &auth); err != nil {
		return &defaultAuth
	}
	if auth.Secret == "" {
		auth.Secret = defaultAuth.Secret
	}
	return &auth
}

func saveAuth(storagePath string, auth *AuthConfig) error {
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(storagePath, "auth.json"), data, 0600)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"expiresAt"`
}

// handleLogin handles POST /api/v1/auth/login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// Authenticate against user store.
	user, err := s.userStore.Authenticate(req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	// Check if this user has MFA enabled.
	full, err := s.userStore.GetUserWithSecrets(req.Username)
	if err == nil && full.MFA.Enabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"mfaRequired": true,
			"username":    req.Username,
		})
		return
	}

	expiresAt := time.Now().Add(24 * time.Hour).Unix()
	token := generateToken(req.Username, expiresAt)

	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		Username:  req.Username,
		Role:      string(user.Role),
		ExpiresAt: expiresAt,
	})
}

// handleChangePassword handles POST /api/v1/auth/change-password
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.NewPassword == "" || len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, "新密码至少 6 位")
		return
	}

	auth := LoadAuth(s.storagePath)
	if req.OldPassword != auth.Password {
		writeError(w, http.StatusUnauthorized, "旧密码错误")
		return
	}

	auth.Password = req.NewPassword
	if err := saveAuth(s.storagePath, auth); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "密码已修改"})
}

// handleCheckAuth handles GET /api/v1/auth/check
func (s *Server) handleCheckAuth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"authenticated": true})
}

// authMiddleware checks JWT token for protected routes.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for login endpoint and static files.
		if r.URL.Path == "/api/v1/auth/login" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "未登录，请先登录")
			return
		}

		username, err := validateToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
			return
		}

		r.Header.Set("X-Username", username)
		next.ServeHTTP(w, r)
	})
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// Simple HMAC-based token (not full JWT, but sufficient for single-server use).
func generateToken(username string, expiresAt int64) string {
	payload := fmt.Sprintf("%s|%d", username, expiresAt)
	sig := sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

func validateToken(token string) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid token format")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid token encoding")
	}

	payload := string(payloadBytes)
	expectedSig := sign(payload)
	if parts[1] != expectedSig {
		return "", fmt.Errorf("invalid token signature")
	}

	// Parse payload.
	pparts := strings.SplitN(payload, "|", 2)
	if len(pparts) != 2 {
		return "", fmt.Errorf("invalid token payload")
	}

	var expiresAt int64
	fmt.Sscanf(pparts[1], "%d", &expiresAt)
	if time.Now().Unix() > expiresAt {
		return "", fmt.Errorf("token expired")
	}

	return pparts[0], nil
}

func sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(defaultAuth.Secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
