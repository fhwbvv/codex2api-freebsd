package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Grok Device Code 授权（RFC 8628），与官方 Grok CLI 一致。
// 浏览器 redirect PKCE 在 xAI 侧会展示「复制代码到 Grok Build」页面，
// 且得到的 token 形态/受众与 device flow 不完全一致；生产路径应走 device flow。

const (
	GrokDeviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	GrokDiscoveryURL        = GrokDefaultOIDCIssuer + "/.well-known/openid-configuration"
)

// GrokDeviceCodeResponse 是 device_authorization 端点的响应。
type GrokDeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	TokenEndpoint           string `json:"-"`
}

// GrokOIDCDiscovery 解析 OIDC discovery 中与 device flow 相关的端点。
type GrokOIDCDiscovery struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
}

// DiscoverGrokOIDC 拉取 xAI OIDC discovery。
func DiscoverGrokOIDC(ctx context.Context, proxyURL string) (*GrokOIDCDiscovery, error) {
	client, err := grokHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GrokDiscoveryURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("grok OIDC discovery 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grok OIDC discovery 失败 (status=%d)", resp.StatusCode)
	}
	var doc GrokOIDCDiscovery
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("grok OIDC discovery 解析失败: %w", err)
	}
	if strings.TrimSpace(doc.DeviceAuthorizationEndpoint) == "" {
		return nil, fmt.Errorf("grok OIDC discovery 缺少 device_authorization_endpoint")
	}
	if strings.TrimSpace(doc.TokenEndpoint) == "" {
		doc.TokenEndpoint = GrokDefaultTokenURL
	}
	return &doc, nil
}

// StartGrokDeviceFlow 向 xAI 申请 device_code / user_code。
func StartGrokDeviceFlow(ctx context.Context, proxyURL string) (*GrokDeviceCodeResponse, error) {
	discovery, err := DiscoverGrokOIDC(ctx, proxyURL)
	if err != nil {
		return nil, err
	}
	client, err := grokHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"client_id": {GrokDefaultOAuthClientID},
		"scope":     {GrokDefaultOAuthScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discovery.DeviceAuthorizationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("grok device code 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grok device code 失败 (status=%d): %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	var device GrokDeviceCodeResponse
	if err := json.Unmarshal(body, &device); err != nil {
		return nil, fmt.Errorf("grok device code 解析失败: %w", err)
	}
	if strings.TrimSpace(device.DeviceCode) == "" || strings.TrimSpace(device.UserCode) == "" {
		return nil, fmt.Errorf("grok device code 响应缺少 device_code/user_code")
	}
	if strings.TrimSpace(device.VerificationURI) == "" && strings.TrimSpace(device.VerificationURIComplete) == "" {
		return nil, fmt.Errorf("grok device code 响应缺少 verification URI")
	}
	device.TokenEndpoint = discovery.TokenEndpoint
	return &device, nil
}

// GrokDevicePollResult 是一次 device token 轮询结果。
type GrokDevicePollResult struct {
	Pending      bool // authorization_pending / slow_down
	SlowDown     bool
	Token        *GrokTokenData
	Email        string
	Subject      string
	TokenEndpoint string
}

// PollGrokDeviceToken 用 device_code 换 token（单次，非阻塞）。
// pending=true 表示用户尚未完成授权，调用方应按 interval 继续轮询。
func PollGrokDeviceToken(ctx context.Context, deviceCode, tokenEndpoint, proxyURL string) (*GrokDevicePollResult, error) {
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return nil, fmt.Errorf("device_code 为空")
	}
	if strings.TrimSpace(tokenEndpoint) == "" {
		tokenEndpoint = GrokDefaultTokenURL
	}
	client, err := grokHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":  {GrokDeviceCodeGrantType},
		"device_code": {deviceCode},
		"client_id":   {GrokDefaultOAuthClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("grok device token 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		IDToken          string `json:"id_token"`
		ExpiresIn        int64  `json:"expires_in"`
	}
	_ = json.Unmarshal(body, &payload)

	if payload.Error != "" {
		switch payload.Error {
		case "authorization_pending":
			return &GrokDevicePollResult{Pending: true}, nil
		case "slow_down":
			return &GrokDevicePollResult{Pending: true, SlowDown: true}, nil
		case "expired_token":
			return nil, fmt.Errorf("device code 已过期，请重新发起授权")
		case "access_denied":
			return nil, fmt.Errorf("用户拒绝了授权")
		default:
			desc := strings.TrimSpace(payload.ErrorDescription)
			if desc != "" {
				return nil, fmt.Errorf("device token 错误: %s: %s", payload.Error, desc)
			}
			return nil, fmt.Errorf("device token 错误: %s", payload.Error)
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device token 失败 (status=%d): %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("device token 响应缺少 access_token")
	}
	if strings.TrimSpace(payload.RefreshToken) == "" {
		return nil, fmt.Errorf("device token 响应缺少 refresh_token")
	}

	expiresIn := payload.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 21600
	}
	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
	if claims := grokJWTClaims(payload.AccessToken); claims != nil {
		if exp, ok := claims["exp"].(float64); ok && exp > 0 {
			expiresAt = time.Unix(int64(exp), 0)
		}
	}
	email, subject := parseGrokIDTokenIdentity(payload.IDToken)
	if subject == "" {
		subject = GrokSubjectFromAccessToken(payload.AccessToken)
	}
	return &GrokDevicePollResult{
		Token: &GrokTokenData{
			AccessToken:  payload.AccessToken,
			RefreshToken: payload.RefreshToken,
			IDToken:      payload.IDToken,
			ExpiresAt:    expiresAt,
		},
		Email:         email,
		Subject:       subject,
		TokenEndpoint: tokenEndpoint,
	}, nil
}

func grokHTTPClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := ConfigureTransportProxy(transport, proxyURL, nil); err != nil {
		return nil, fmt.Errorf("代理配置失败: %w", err)
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

func parseGrokIDTokenIdentity(idToken string) (email, subject string) {
	claims := grokJWTClaims(idToken)
	if claims == nil {
		return "", ""
	}
	return grokClaimString(claims, "email"), grokClaimString(claims, "sub")
}

func truncateStr(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
