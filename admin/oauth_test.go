package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

func TestExchangeOAuthCodeSeedsAccessTokenFromExchangeResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)
	handler := &Handler{db: db, store: store}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "access-from-exchange",
			"refresh_token": "refresh-from-exchange",
			"id_token": "id-from-exchange",
			"expires_in": 3600
		}`))
	}))
	defer server.Close()

	oldResinCfg := proxy.GetResinConfig()
	oldDecorator := auth.ResinRequestDecorator
	proxy.SetResinConfig(&proxy.ResinConfig{BaseURL: server.URL, PlatformName: "codex2api"})
	t.Cleanup(func() {
		proxy.SetResinConfig(oldResinCfg)
		auth.ResinRequestDecorator = oldDecorator
	})

	sessionID := "oauth-test-session"
	globalOAuthStore.set(sessionID, &oauthSession{
		State:        "state-test",
		CodeVerifier: "verifier-test",
		RedirectURI:  oauthDefaultRedirectURI,
		CreatedAt:    time.Now(),
	})
	t.Cleanup(func() {
		globalOAuthStore.delete(sessionID)
	})

	body := `{"session_id":"oauth-test-session","code":"code-test","state":"state-test"}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/oauth/exchange-code", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ExchangeOAuthCode(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ID == 0 {
		t.Fatal("response id is empty")
	}

	account := store.FindByID(payload.ID)
	if account == nil {
		t.Fatalf("runtime account %d not found", payload.ID)
	}
	account.Mu().RLock()
	accessToken := account.AccessToken
	refreshToken := account.RefreshToken
	account.Mu().RUnlock()
	if accessToken != "access-from-exchange" || refreshToken != "refresh-from-exchange" {
		t.Fatalf("runtime tokens = access:%q refresh:%q, want exchange tokens", accessToken, refreshToken)
	}

	row, err := db.GetAccountByID(context.Background(), payload.ID)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if got := row.GetCredential("access_token"); got != "access-from-exchange" {
		t.Fatalf("stored access_token = %q, want exchange access token", got)
	}
	if got := row.GetCredential("id_token"); got != "id-from-exchange" {
		t.Fatalf("stored id_token = %q, want exchange id token", got)
	}
}

func newOAuthExchangeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "access-from-exchange",
			"refresh_token": "refresh-from-exchange",
			"id_token": "id-from-exchange",
			"expires_in": 3600
		}`))
	}))
	t.Cleanup(server.Close)

	oldResinCfg := proxy.GetResinConfig()
	oldDecorator := auth.ResinRequestDecorator
	proxy.SetResinConfig(&proxy.ResinConfig{BaseURL: server.URL, PlatformName: "codex2api"})
	t.Cleanup(func() {
		proxy.SetResinConfig(oldResinCfg)
		auth.ResinRequestDecorator = oldDecorator
	})
	return server
}

func TestExchangeOAuthCodeTriggersUsageProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)
	probed := make(chan int64, 1)
	handler := &Handler{db: db, store: store}
	handler.probeUsage = func(_ context.Context, account *auth.Account) error {
		probed <- account.DBID
		return nil
	}

	newOAuthExchangeTestServer(t)

	sessionID := "oauth-probe-session"
	globalOAuthStore.set(sessionID, &oauthSession{
		State:        "state-probe",
		CodeVerifier: "verifier-probe",
		RedirectURI:  oauthDefaultRedirectURI,
		CreatedAt:    time.Now(),
	})
	t.Cleanup(func() {
		globalOAuthStore.delete(sessionID)
	})

	body := `{"session_id":"oauth-probe-session","code":"code-probe","state":"state-probe"}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/oauth/exchange-code", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ExchangeOAuthCode(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	select {
	case dbID := <-probed:
		if dbID != payload.ID {
			t.Fatalf("usage probe ran for account %d, want %d", dbID, payload.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("usage probe was not triggered after OAuth account add")
	}
}

func TestOAuthCallbackTriggersUsageProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)
	probed := make(chan int64, 1)
	handler := &Handler{db: db, store: store}
	handler.probeUsage = func(_ context.Context, account *auth.Account) error {
		probed <- account.DBID
		return nil
	}

	newOAuthExchangeTestServer(t)

	sessionID := "oauth-callback-probe-session"
	sess := &oauthSession{
		State:        "state-callback-probe",
		CodeVerifier: "verifier-callback-probe",
		RedirectURI:  oauthDefaultRedirectURI,
		CreatedAt:    time.Now(),
	}
	globalOAuthStore.set(sessionID, sess)
	t.Cleanup(func() {
		globalOAuthStore.delete(sessionID)
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/oauth/callback?code=code-callback-probe&state=state-callback-probe", nil)

	handler.OAuthCallback(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if sess.ExchangeResult == nil || !sess.ExchangeResult.Success {
		t.Fatalf("exchange result = %+v, want success", sess.ExchangeResult)
	}

	select {
	case dbID := <-probed:
		if dbID != sess.ExchangeResult.ID {
			t.Fatalf("usage probe ran for account %d, want %d", dbID, sess.ExchangeResult.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("usage probe was not triggered after OAuth callback account add")
	}
}
