package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/configmap-sync/configmap-sync/internal/store"
)

// MFAConfig is retained as a type alias for backward compatibility with auth.json.
type MFAConfig = store.MFASettings

// handleMFAStatus returns whether MFA is enabled for the current user.
func (s *Server) handleMFAStatus(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Username")
	enabled := false
	if s.userStore != nil && username != "" {
		if u, err := s.userStore.GetUser(username); err == nil {
			enabled = u.MFA.Enabled
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": enabled})
}

// handleMFASetup generates a new TOTP secret for the current user and returns
// the provisioning URI so a QR code can be rendered.
func (s *Server) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Username")
	if s.userStore == nil || username == "" {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}

	user, err := s.userStore.GetUserWithSecrets(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if user.MFA.Enabled {
		writeError(w, http.StatusBadRequest, "MFA 已启用，请先禁用再重新设置")
		return
	}

	// Generate a fresh 20-byte random secret.
	secret := generateTOTPSecret()
	mfa := store.MFASettings{Enabled: false, Secret: secret}
	if err := s.userStore.SetUserMFA(username, mfa); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}

	issuer := "YamlSync"
	otpauthURL := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		url.PathEscape(issuer), url.PathEscape(username), secret, url.QueryEscape(issuer))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"secret":     secret,
		"otpauthURL": otpauthURL,
		"issuer":     issuer,
		"account":    username,
	})
}

// handleMFAEnable verifies the TOTP code and enables MFA for the current user.
func (s *Server) handleMFAEnable(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Username")
	if s.userStore == nil || username == "" {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	user, err := s.userStore.GetUserWithSecrets(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if user.MFA.Secret == "" {
		writeError(w, http.StatusBadRequest, "请先调用 setup 生成密钥")
		return
	}
	if !verifyTOTP(user.MFA.Secret, req.Code) {
		writeError(w, http.StatusUnauthorized, "验证码错误，请重试")
		return
	}

	mfa := user.MFA
	mfa.Enabled = true
	if err := s.userStore.SetUserMFA(username, mfa); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "MFA 已启用"})
}

// handleMFADisable disables MFA for the current user (requires a valid TOTP code).
func (s *Server) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Username")
	if s.userStore == nil || username == "" {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	user, err := s.userStore.GetUserWithSecrets(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if !user.MFA.Enabled {
		writeError(w, http.StatusBadRequest, "MFA 未启用")
		return
	}
	if !verifyTOTP(user.MFA.Secret, req.Code) {
		writeError(w, http.StatusUnauthorized, "验证码错误")
		return
	}

	if err := s.userStore.SetUserMFA(username, store.MFASettings{Enabled: false, Secret: ""}); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "MFA 已禁用"})
}

// handleMFAVerify verifies TOTP code during login (second step).
func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if s.userStore == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}

	user, err := s.userStore.GetUserWithSecrets(req.Username)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "用户不存在")
		return
	}
	if !user.MFA.Enabled {
		writeError(w, http.StatusBadRequest, "MFA 未启用")
		return
	}
	if !verifyTOTP(user.MFA.Secret, req.Code) {
		writeError(w, http.StatusUnauthorized, "验证码错误")
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

// ---- Admin endpoint: reset another user's MFA ----

// handleResetUserMFA clears MFA settings for a target user (admin only).
func (s *Server) handleResetUserMFA(w http.ResponseWriter, r *http.Request, username string) {
	if s.userStore == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}
	if _, err := s.userStore.GetUser(username); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.userStore.SetUserMFA(username, store.MFASettings{Enabled: false, Secret: ""}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "MFA 已重置"})
}

// handleSetUserMFAEnabled lets an admin enable/disable MFA for a target user.
// Enabling requires the user to have previously completed MFA setup (i.e., a secret must exist).
func (s *Server) handleSetUserMFAEnabled(w http.ResponseWriter, r *http.Request, username string) {
	if s.userStore == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	user, err := s.userStore.GetUserWithSecrets(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}

	if req.Enabled {
		// Can only enable if user has previously set up MFA (has a secret).
		if user.MFA.Secret == "" {
			writeError(w, http.StatusBadRequest, "该用户尚未设置 MFA，请让用户自行在安全认证页面扫码设置")
			return
		}
		if err := s.userStore.SetUserMFA(username, store.MFASettings{Enabled: true, Secret: user.MFA.Secret}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "MFA 已启用"})
		return
	}

	// Disable: keep the secret so admin can re-enable later without user re-setup.
	if err := s.userStore.SetUserMFA(username, store.MFASettings{Enabled: false, Secret: user.MFA.Secret}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "MFA 已关闭"})
}

// ---- TOTP Implementation (RFC 6238) ----

func generateTOTPSecret() string {
	secret := make([]byte, 20)
	_, _ = rand.Read(secret)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

func generateTOTP(secret string, t time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}

	// Time step: 30 seconds.
	counter := uint64(t.Unix()) / 30

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0f
	code := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff
	otp := code % 1000000
	return fmt.Sprintf("%06d", otp)
}

func verifyTOTP(secret, code string) bool {
	now := time.Now()
	// Allow ±1 time step (30s window each side) for clock skew.
	for _, offset := range []int{-1, 0, 1} {
		t := now.Add(time.Duration(offset) * 30 * time.Second)
		if generateTOTP(secret, t) == code {
			return true
		}
	}
	return false
}
