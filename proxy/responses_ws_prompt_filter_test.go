package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

func TestResponsesWebSocketLocalBlockUsesConfiguredMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newConfiguredLocalBlockMessageHandler("Blocked by Example Gateway")
	body := []byte("{\"type\":\"response.create\",\"model\":\"gpt-5.5\",\"input\":\"blocked-content\"}")
	done := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			close(done)
			return
		}
		defer conn.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r
		blocked, delegated := handler.inspectPromptFilterOpenAIForWebSocket(c, conn, body, "/v1/responses", "gpt-5.5", "responses:1")
		if !blocked || delegated {
			t.Errorf("blocked=%v delegated=%v", blocked, delegated)
		}
		close(done)
	}))
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read error frame: %v", err)
	}
	if got := gjson.GetBytes(payload, "error.message").String(); got != "Blocked by Example Gateway" {
		t.Fatalf("message = %q, payload=%s", got, payload)
	}
	if got := gjson.GetBytes(payload, "error.code").String(); got != "prompt_blocked" {
		t.Fatalf("code = %q, payload=%s", got, payload)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server handler did not finish")
	}
}
