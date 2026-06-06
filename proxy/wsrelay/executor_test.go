package wsrelay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/proxy"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

func TestPrepareWebsocketHeadersUsesConfiguredDefaultsAndBetaFeatures(t *testing.T) {
	t.Setenv("CODEX_WS_SEND_USER_AGENT", "true")
	exec := NewExecutor()
	cfg := &proxy.DeviceProfileConfig{
		UserAgent:              "codex_cli_rs/0.120.0 (Mac OS 15.5.0; arm64) Apple_Terminal/464",
		PackageVersion:         "0.120.0",
		RuntimeVersion:         "0.120.0",
		OS:                     "MacOS",
		Arch:                   "arm64",
		StabilizeDeviceProfile: true,
		BetaFeatures:           "multi_agent",
	}
	ginHeaders := http.Header{
		"Originator": []string{"custom-originator"},
	}

	headers := exec.prepareWebsocketHeaders("token-123", "42", "session-123", "api-key-1", cfg, ginHeaders)

	if got := headers.Get("Authorization"); got != "Bearer token-123" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Get("OpenAI-Beta"); got != responsesWebsocketBetaHeader {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	if got := headers.Get("X-Codex-Beta-Features"); got != "multi_agent" {
		t.Fatalf("X-Codex-Beta-Features = %q", got)
	}
	if got := headers.Get("User-Agent"); got != cfg.UserAgent {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := headers.Get("Version"); got != "0.120.0" {
		t.Fatalf("Version = %q", got)
	}
	if got := headers.Get("Originator"); got != proxy.Originator {
		t.Fatalf("Originator = %q", got)
	}
	if got := headers.Get("Chatgpt-Account-Id"); got != "42" {
		t.Fatalf("Chatgpt-Account-Id = %q", got)
	}
	if got := headers.Get("Conversation_id"); got != "session-123" {
		t.Fatalf("Conversation_id = %q", got)
	}
	if got := headers.Get("Session_id"); got != "session-123" {
		t.Fatalf("Session_id = %q", got)
	}
}

func TestPrepareWebsocketHeadersOmitsUserAgentByDefault(t *testing.T) {
	exec := NewExecutor()
	ginHeaders := http.Header{
		"X-Codex-Turn-State":                    []string{"turn-state"},
		"X-Codex-Turn-Metadata":                 []string{"turn-metadata"},
		"X-Client-Request-Id":                   []string{"req-123"},
		"X-Responsesapi-Include-Timing-Metrics": []string{"true"},
	}

	headers := exec.prepareWebsocketHeaders("token-123", "42", "session-123", "api-key-1", nil, ginHeaders)

	if got := headers.Get("User-Agent"); got != "" {
		t.Fatalf("User-Agent = %q, want empty", got)
	}
	if got := headers.Get("Version"); got != "" {
		t.Fatalf("Version = %q, want empty", got)
	}
	if got := headers.Get("OpenAI-Beta"); got != responsesWebsocketBetaHeader {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	for _, name := range []string{"X-Codex-Turn-State", "X-Codex-Turn-Metadata", "X-Client-Request-Id", "X-Responsesapi-Include-Timing-Metrics"} {
		if got := headers.Get(name); got != ginHeaders.Get(name) {
			t.Fatalf("%s = %q, want %q", name, got, ginHeaders.Get(name))
		}
	}
	if got := headers.Get("Session_id"); got != "session-123" {
		t.Fatalf("Session_id = %q", got)
	}
	if got := headers.Get("Conversation_id"); got != "session-123" {
		t.Fatalf("Conversation_id = %q", got)
	}
}

func TestPrepareWebsocketBodyPreservesPreviousResponseID(t *testing.T) {
	exec := NewExecutor()

	got := exec.prepareWebsocketBody([]byte(`{"model":"gpt-5.4","previous_response_id":"resp_123","input":[{"role":"user","content":"continue"}]}`), "session-123")

	if prev := gjson.GetBytes(got, "previous_response_id").String(); prev != "resp_123" {
		t.Fatalf("previous_response_id = %q, want resp_123; body=%s", prev, got)
	}
	if cacheKey := gjson.GetBytes(got, "prompt_cache_key").String(); cacheKey != "session-123" {
		t.Fatalf("prompt_cache_key = %q, want session-123; body=%s", cacheKey, got)
	}
	if typ := gjson.GetBytes(got, "type").String(); typ != "response.create" {
		t.Fatalf("type = %q, want response.create; body=%s", typ, got)
	}
	if !gjson.GetBytes(got, "stream").Bool() {
		t.Fatalf("stream should be true; body=%s", got)
	}
}

func TestNormalizeWebsocketHandshakeResponse(t *testing.T) {
	t.Run("switching protocols is successful websocket handshake", func(t *testing.T) {
		statusCode, _, failed := normalizeWebsocketHandshakeResponse(&http.Response{
			StatusCode: http.StatusSwitchingProtocols,
		})
		if failed {
			t.Fatal("failed = true, want false")
		}
		if statusCode != http.StatusOK {
			t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusOK)
		}
	})

	t.Run("http 2xx is normalized for downstream handler", func(t *testing.T) {
		statusCode, _, failed := normalizeWebsocketHandshakeResponse(&http.Response{
			StatusCode: http.StatusNoContent,
		})
		if failed {
			t.Fatal("failed = true, want false")
		}
		if statusCode != http.StatusOK {
			t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusOK)
		}
	})

	t.Run("non success status remains a handshake failure", func(t *testing.T) {
		statusCode, _, failed := normalizeWebsocketHandshakeResponse(&http.Response{
			StatusCode: http.StatusUnauthorized,
		})
		if !failed {
			t.Fatal("failed = false, want true")
		}
		if statusCode != http.StatusUnauthorized {
			t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusUnauthorized)
		}
	})
}

func TestWebsocketResponseToHTTPClosesBodyOnContextCancel(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	session := NewSession(1, nil)
	pr := session.AddPendingRequest("session-1")
	wc := NewWsConnection(conn, session, wsURL)
	manager := NewManager()
	defer manager.Stop()
	wsResp := &WsResponse{
		conn:        wc,
		pendingReq:  pr,
		sessionID:   "session-1",
		manager:     manager,
		readErrChan: make(chan error, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	resp := websocketResponseToHTTP(ctx, wsResp, http.StatusOK, http.Header{})
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := resp.Body.Read(make([]byte, 1))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Body.Read returned nil error after context cancellation")
		}
		if err != context.Canceled && err != io.ErrClosedPipe && !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("Body.Read error = %v, want context cancellation or closed pipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Body.Read stayed blocked after context cancellation")
	}
}

func TestSendRequestWritesResponseCreatePayloadDirectly(t *testing.T) {
	received := make(chan []byte, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read websocket message: %v", err)
			return
		}
		received <- payload
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	exec := NewExecutor()
	wc := NewWsConnection(conn, NewSession(1, nil), wsURL)
	body := []byte(`{"type":"response.create","model":"gpt-5.4","input":"hi","stream":true}`)
	if err := exec.sendRequest(wc, body, "request-1"); err != nil {
		t.Fatalf("sendRequest: %v", err)
	}

	got := <-received
	if string(got) != string(body) {
		t.Fatalf("sent payload = %s, want %s", got, body)
	}
	if eventType := gjson.GetBytes(got, "type").String(); eventType != "response.create" {
		t.Fatalf("sent type = %q, want response.create; payload=%s", eventType, got)
	}
	if gjson.GetBytes(got, "request_id").Exists() {
		t.Fatalf("payload should not contain internal request_id wrapper: %s", got)
	}
}
