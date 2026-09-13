// Package feishu implements a minimal Feishu (Lark) OAuth login and message-send client.
// All app credentials come from configuration (never hard-coded).
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const base = "https://open.feishu.cn"

// Client talks to the Feishu open API using an app id/secret.
type Client struct {
	appID     string
	appSecret string
	http      *http.Client
}

func NewClient(appID, appSecret string) *Client {
	return &Client{
		appID:     appID,
		appSecret: appSecret,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// AuthURL builds the Feishu authorization URL to redirect the user to.
// state is an opaque value echoed back to the callback.
func (c *Client) AuthURL(redirectURI, state string) string {
	q := url.Values{}
	q.Set("app_id", c.appID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	// Authorization code flow for web/QR login.
	return base + "/open-apis/authen/v1/index?" + q.Encode()
}

// appAccessToken fetches an app_access_token (internal app).
func (c *Client) appAccessToken(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})
	var out struct {
		Code           int    `json:"code"`
		Msg            string `json:"msg"`
		AppAccessToken string `json:"app_access_token"`
	}
	if err := c.postJSON(ctx, "/open-apis/auth/v3/app_access_token/internal", "", body, &out); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("feishu app_access_token error %d: %s", out.Code, out.Msg)
	}
	return out.AppAccessToken, nil
}

// UserInfo is the subset of Feishu user info we need.
type UserInfo struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	UserID string `json:"user_id"`
	OpenID string `json:"open_id"`
}

// ExchangeCode exchanges an auth code for the logged-in user's info.
func (c *Client) ExchangeCode(ctx context.Context, code string) (*UserInfo, error) {
	appToken, err := c.appAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	// Exchange code -> access_token + user identity.
	reqBody, _ := json.Marshal(map[string]string{
		"grant_type": "authorization_code",
		"code":       code,
	})
	var tok struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken string `json:"access_token"`
			Name        string `json:"name"`
			Email       string `json:"email"`
			UserID      string `json:"user_id"`
			OpenID      string `json:"open_id"`
		} `json:"data"`
	}
	if err := c.postJSON(ctx, "/open-apis/authen/v1/oidc/access_token", appToken, reqBody, &tok); err != nil {
		return nil, err
	}
	if tok.Code != 0 {
		return nil, fmt.Errorf("feishu access_token error %d: %s", tok.Code, tok.Msg)
	}
	return &UserInfo{
		Name:   tok.Data.Name,
		Email:  tok.Data.Email,
		UserID: tok.Data.UserID,
		OpenID: tok.Data.OpenID,
	}, nil
}

// SendText sends a plain-text message to a user identified by open_id (preferred)
// or user_id. Returns nil on success.
func (c *Client) SendText(ctx context.Context, openID, userID, text string) error {
	appToken, err := c.appAccessToken(ctx)
	if err != nil {
		return err
	}
	idType := "open_id"
	receiveID := openID
	if receiveID == "" {
		idType = "user_id"
		receiveID = userID
	}
	if receiveID == "" {
		return fmt.Errorf("no feishu id to send message to")
	}
	content, _ := json.Marshal(map[string]string{"text": text})
	reqBody, _ := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	})
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := c.postJSON(ctx, "/open-apis/im/v1/messages?receive_id_type="+idType, appToken, reqBody, &out); err != nil {
		return err
	}
	if out.Code != 0 {
		return fmt.Errorf("feishu send message error %d: %s", out.Code, out.Msg)
	}
	return nil
}

func (c *Client) postJSON(ctx context.Context, path, bearer string, body []byte, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return json.Unmarshal(data, out)
}
