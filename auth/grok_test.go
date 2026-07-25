package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// makeJWT 构造一个仅含指定 claims 的未签名 JWT（header.payload.sig 三段）。
func makeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}

func TestParseGrokAuthJSONFlatOAuth(t *testing.T) {
	access := makeJWT(map[string]any{"sub": "user-123", "client_id": "cid-xyz", "exp": float64(time.Now().Add(time.Hour).Unix())})
	raw := []byte(`{"access_token":"` + access + `","refresh_token":"rt-abc","client_id":"cid-explicit"}`)

	creds, err := ParseGrokAuthJSON(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("期望 1 条凭据，得到 %d", len(creds))
	}
	c := creds[0]
	if c.AuthKind() != GrokAuthKindOAuth {
		t.Fatalf("期望 oauth，得到 %s", c.AuthKind())
	}
	if c.RefreshToken != "rt-abc" {
		t.Fatalf("refresh_token 不符: %q", c.RefreshToken)
	}
	if c.ClientID != "cid-explicit" {
		t.Fatalf("显式 client_id 应优先，得到 %q", c.ClientID)
	}
	if c.Subject != "user-123" {
		t.Fatalf("subject 应从 JWT 解出，得到 %q", c.Subject)
	}
	if c.ExpiresAt.IsZero() {
		t.Fatalf("expires_at 应从 JWT exp 解出")
	}
}

func TestParseGrokAuthJSONAPIKeyByScope(t *testing.T) {
	raw := []byte(`{"tokens":{"xai::api_key":{"key":"xai-secret-key"}}}`)
	creds, err := ParseGrokAuthJSON(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("期望 1 条凭据，得到 %d", len(creds))
	}
	if creds[0].AuthKind() != GrokAuthKindAPIKey {
		t.Fatalf("期望 api_key，得到 %s", creds[0].AuthKind())
	}
	if creds[0].APIKey != "xai-secret-key" {
		t.Fatalf("api_key 不符: %q", creds[0].APIKey)
	}
}

func TestParseGrokAuthJSONExplicitAPIKeyMode(t *testing.T) {
	raw := []byte(`{"key":"xai-k","auth_mode":"api_key"}`)
	creds, err := ParseGrokAuthJSON(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if creds[0].AuthKind() != GrokAuthKindAPIKey || creds[0].APIKey != "xai-k" {
		t.Fatalf("显式 api_key 模式解析错误: %+v", creds[0])
	}
}

func TestParseGrokAuthJSONOAuthMissingRefreshFails(t *testing.T) {
	access := makeJWT(map[string]any{"sub": "user-1"})
	raw := []byte(`{"access_token":"` + access + `"}`)
	if _, err := ParseGrokAuthJSON(raw); err == nil {
		t.Fatalf("OAuth 凭据缺 refresh_token 应报错")
	}
}

func TestParseGrokAuthJSONInvalid(t *testing.T) {
	if _, err := ParseGrokAuthJSON([]byte(`not json`)); err == nil {
		t.Fatalf("非法 JSON 应报错")
	}
	if _, err := ParseGrokAuthJSON([]byte(`{}`)); err == nil {
		t.Fatalf("空对象应报错")
	}
}

func TestNormalizeGrokBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"https://api.x.ai/v1/":    "https://api.x.ai/v1",
		"https://api.x.ai/v1?x=1": "https://api.x.ai/v1",
	}
	for in, want := range cases {
		got, err := NormalizeGrokBaseURL(in)
		if err != nil {
			t.Fatalf("NormalizeGrokBaseURL(%q) 报错: %v", in, err)
		}
		if got != want {
			t.Fatalf("NormalizeGrokBaseURL(%q) = %q, 期望 %q", in, got, want)
		}
	}
	if _, err := NormalizeGrokBaseURL("ftp://x"); err == nil {
		t.Fatalf("非 http/https 应报错")
	}
	if _, err := NormalizeGrokBaseURL("not a url"); err == nil {
		t.Fatalf("非法 URL 应报错")
	}
}

func TestAccountGrokCredentialsEndpointSelection(t *testing.T) {
	apiKeyAcc := &Account{UpstreamType: UpstreamGrok, APIKey: "xai-k"}
	base, bearer := apiKeyAcc.GrokCredentials()
	if base != GrokDefaultAPIBaseURL || bearer != "xai-k" {
		t.Fatalf("API Key 账号应走 %s，得到 base=%q bearer=%q", GrokDefaultAPIBaseURL, base, bearer)
	}
	if apiKeyAcc.GrokAuthKind() != GrokAuthKindAPIKey {
		t.Fatalf("应识别为 api_key")
	}

	oauthAcc := &Account{UpstreamType: UpstreamGrok, AccessToken: "at-1", RefreshToken: "rt-1"}
	base, bearer = oauthAcc.GrokCredentials()
	if base != GrokDefaultChatProxyBaseURL || bearer != "at-1" {
		t.Fatalf("OAuth 账号应走 %s，得到 base=%q bearer=%q", GrokDefaultChatProxyBaseURL, base, bearer)
	}

	custom := &Account{UpstreamType: UpstreamGrok, APIKey: "xai-k", BaseURL: "https://proxy.example/v1"}
	base, _ = custom.GrokCredentials()
	if base != "https://proxy.example/v1" {
		t.Fatalf("base_url 覆盖应生效，得到 %q", base)
	}

	if !oauthAcc.IsGrokAPI() || !oauthAcc.IsRelayStyle() {
		t.Fatalf("Grok 账号应同时满足 IsGrokAPI 和 IsRelayStyle")
	}
	if (&Account{}).IsGrokAPI() {
		t.Fatalf("空账号不应是 Grok")
	}
}

func TestSanitizeGrokBaseURLLowerNoPanic(t *testing.T) {
	// 确保对大小写 scheme 的处理不 panic
	if _, err := NormalizeGrokBaseURL("HTTPS://API.X.AI/v1"); err != nil {
		t.Fatalf("大写 scheme 应被接受: %v", err)
	}
	_ = strings.ToLower("")
}

func TestBuildGrokAuthorizationURL(t *testing.T) {
	url, err := BuildGrokAuthorizationURL(GrokAuthURLParams{
		State:         "state-abc",
		CodeChallenge: "challenge-xyz",
		Nonce:         "nonce-1",
	})
	if err != nil {
		t.Fatalf("BuildGrokAuthorizationURL: %v", err)
	}
	if !strings.HasPrefix(url, GrokDefaultAuthorizeURL+"?") {
		t.Fatalf("unexpected prefix: %s", url)
	}
	if !strings.Contains(url, "code_challenge=challenge-xyz") {
		t.Fatalf("missing challenge: %s", url)
	}
	if !strings.Contains(url, "client_id="+GrokDefaultOAuthClientID) {
		t.Fatalf("missing client_id: %s", url)
	}
	if !strings.Contains(url, "redirect_uri=") {
		t.Fatalf("missing redirect_uri: %s", url)
	}
	if !strings.Contains(url, "referrer=codex2api") {
		t.Fatalf("missing referrer: %s", url)
	}
}

func TestBuildGrokAuthorizationURLRequiresState(t *testing.T) {
	if _, err := BuildGrokAuthorizationURL(GrokAuthURLParams{CodeChallenge: "x"}); err == nil {
		t.Fatal("expected error for empty state")
	}
}

func TestParseGrokAuthorizationInput(t *testing.T) {
	cases := []struct {
		name          string
		raw           string
		wantCode      string
		wantState     string
		wantRequires  bool
	}{
		{
			name:         "full callback url",
			raw:          "http://127.0.0.1:56121/callback?code=abc123&state=s1",
			wantCode:     "abc123",
			wantState:    "s1",
			wantRequires: true,
		},
		{
			name:         "query string",
			raw:          "code=xyz&state=s2",
			wantCode:     "xyz",
			wantState:    "s2",
			wantRequires: true,
		},
		{
			name:     "bare code",
			raw:      "bare-code-only",
			wantCode: "bare-code-only",
		},
		{
			name: "empty",
			raw:  "  ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseGrokAuthorizationInput(tc.raw)
			if got.Code != tc.wantCode || got.State != tc.wantState || got.RequiresState != tc.wantRequires {
				t.Fatalf("got %+v, want code=%q state=%q requires=%v", got, tc.wantCode, tc.wantState, tc.wantRequires)
			}
		})
	}
}

func TestGrokSubjectFromAccessToken(t *testing.T) {
	token := makeJWT(map[string]any{"sub": "user-sub-9"})
	if got := GrokSubjectFromAccessToken(token); got != "user-sub-9" {
		t.Fatalf("got %q", got)
	}
	if got := GrokSubjectFromAccessToken("not-a-jwt"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// Grok 账号页的一键清理只应删除 Grok 账号，同状态的其它平台账号必须保留。
func TestCleanGrokByRuntimeStatusOnlyTouchesGrokAccounts(t *testing.T) {
	grokBanned := &Account{
		DBID:         1,
		UpstreamType: UpstreamGrok,
		APIKey:       "xai-banned",
		Status:       StatusReady,
		HealthTier:   HealthTierBanned,
	}
	codexBanned := &Account{
		DBID:        2,
		AccessToken: "codex-banned",
		Status:      StatusReady,
		HealthTier:  HealthTierBanned,
	}
	grokError := &Account{
		DBID:         3,
		UpstreamType: UpstreamGrok,
		APIKey:       "xai-error",
		Status:       StatusError,
	}

	store := &Store{accounts: []*Account{grokBanned, codexBanned, grokError}}

	if cleaned := store.CleanGrokByRuntimeStatus(context.Background(), "unauthorized"); cleaned != 1 {
		t.Fatalf("CleanGrokByRuntimeStatus(unauthorized) cleaned = %d, want 1", cleaned)
	}
	if store.FindByID(1) != nil {
		t.Fatal("封禁的 Grok 账号应被清理")
	}
	if store.FindByID(2) == nil {
		t.Fatal("封禁的非 Grok 账号不应被 Grok 清理波及")
	}

	if cleaned := store.CleanGrokByRuntimeStatus(context.Background(), "error"); cleaned != 1 {
		t.Fatalf("CleanGrokByRuntimeStatus(error) cleaned = %d, want 1", cleaned)
	}
	if store.FindByID(3) != nil {
		t.Fatal("错误状态的 Grok 账号应被清理")
	}
}
