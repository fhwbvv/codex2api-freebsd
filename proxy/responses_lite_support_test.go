package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/tidwall/gjson"
)

// resetLearnedResponsesLiteSupport 清空学习表并注册恢复，避免测试间串味。
func resetLearnedResponsesLiteSupport(t *testing.T) {
	t.Helper()
	learnedResponsesLiteSupport.Lock()
	saved := learnedResponsesLiteSupport.bySlug
	learnedResponsesLiteSupport.bySlug = make(map[string]bool)
	learnedResponsesLiteSupport.Unlock()
	t.Cleanup(func() {
		learnedResponsesLiteSupport.Lock()
		learnedResponsesLiteSupport.bySlug = saved
		learnedResponsesLiteSupport.Unlock()
	})
}

func TestResponsesLiteSupportForModelBuiltinSeed(t *testing.T) {
	resetLearnedResponsesLiteSupport(t)

	tests := []struct {
		model        string
		wantSupports bool
		wantKnown    bool
	}{
		{model: "gpt-5.6-sol", wantSupports: true, wantKnown: true},
		{model: " GPT-5.6-TERRA ", wantSupports: true, wantKnown: true},
		{model: "codex-auto-review", wantSupports: true, wantKnown: true},
		{model: "gpt-5.5", wantSupports: false, wantKnown: true},
		{model: "gpt-5.4", wantSupports: false, wantKnown: true},
		{model: "gpt-5.7-nova", wantSupports: false, wantKnown: false},
		{model: "", wantSupports: false, wantKnown: false},
	}
	for _, tt := range tests {
		supports, known := responsesLiteSupportForModel(tt.model)
		if supports != tt.wantSupports || known != tt.wantKnown {
			t.Fatalf("responsesLiteSupportForModel(%q) = (%t,%t), want (%t,%t)",
				tt.model, supports, known, tt.wantSupports, tt.wantKnown)
		}
	}
}

func TestRecordResponsesLiteSupportFromManifest(t *testing.T) {
	resetLearnedResponsesLiteSupport(t)

	manifest := []byte(`{"models":[
		{"slug":"gpt-5.7-nova","use_responses_lite":true},
		{"slug":"gpt-5.6-sol","use_responses_lite":false},
		{"slug":"gpt-5.8-missing-flag"},
		{"slug":"gpt-5.8-bad-flag","use_responses_lite":"yes"},
		{"slug":"","use_responses_lite":true}
	]}`)
	RecordResponsesLiteSupportFromManifest(manifest)

	if supports, known := responsesLiteSupportForModel("gpt-5.7-nova"); !known || !supports {
		t.Fatalf("gpt-5.7-nova = (%t,%t), want learned true", supports, known)
	}
	// 清单是上游真值，学习结果覆盖内置种子。
	if supports, known := responsesLiteSupportForModel("gpt-5.6-sol"); !known || supports {
		t.Fatalf("gpt-5.6-sol = (%t,%t), want learned override to false", supports, known)
	}
	// 字段缺失/非布尔的条目保持未知。
	if _, known := responsesLiteSupportForModel("gpt-5.8-missing-flag"); known {
		t.Fatalf("gpt-5.8-missing-flag should stay unknown")
	}
	if _, known := responsesLiteSupportForModel("gpt-5.8-bad-flag"); known {
		t.Fatalf("gpt-5.8-bad-flag should stay unknown")
	}

	// 非法/空清单不 panic、不改状态。
	RecordResponsesLiteSupportFromManifest(nil)
	RecordResponsesLiteSupportFromManifest([]byte(`{"models":"nope"}`))
}

func TestGateResponsesLiteForModel(t *testing.T) {
	resetLearnedResponsesLiteSupport(t)

	tests := []struct {
		name      string
		requested bool
		body      []byte
		want      bool
	}{
		{name: "not requested stays off", requested: false, body: []byte(`{"model":"gpt-5.6-sol"}`), want: false},
		{name: "known non-lite model strips signal", requested: true, body: []byte(`{"model":"gpt-5.5"}`), want: false},
		{name: "known lite model keeps signal", requested: true, body: []byte(`{"model":"gpt-5.6-sol"}`), want: true},
		{name: "unknown model passes through", requested: true, body: []byte(`{"model":"gpt-5.7-nova"}`), want: true},
		{name: "missing model passes through", requested: true, body: []byte(`{}`), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gateResponsesLiteForModel(tt.requested, tt.body); got != tt.want {
				t.Fatalf("gateResponsesLiteForModel(%t, %s) = %t, want %t", tt.requested, tt.body, got, tt.want)
			}
		})
	}
}

// 回归 issue「请求 gpt-5.5 报 This model is not supported when using
// X-OpenAI-Internal-Codex-Responses-Lite」：下游对 lite 模型发信号，模型被网关
// 改写成非 lite 模型后，信号必须在发出前剥离，而不是原样透传给上游触发 400。
func TestOpenAIResponsesExecutorsStripLiteForKnownNonLiteModel(t *testing.T) {
	resetLearnedResponsesLiteSupport(t)

	type result struct {
		path string
		lite string
	}
	results := make(chan result, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		results <- result{path: r.URL.Path, lite: r.Header.Get(codexResponsesLiteHeader)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test"}`))
	}))
	t.Cleanup(server.Close)

	account := &auth.Account{
		DBID:         77,
		UpstreamType: auth.UpstreamOpenAIResponses,
		BaseURL:      server.URL,
		APIKey:       "relay-token",
	}
	downstreamHeaders := make(http.Header)
	downstreamHeaders.Set(codexResponsesLiteHeader, "true")

	resp, err := ExecuteOpenAIResponsesRequest(context.Background(), account, []byte(`{"model":"gpt-5.5"}`), "", downstreamHeaders)
	if err != nil {
		t.Fatalf("ExecuteOpenAIResponsesRequest() error = %v", err)
	}
	resp.Body.Close()

	resp, err = ExecuteOpenAIResponsesCompactRequest(context.Background(), account, []byte(`{"model":"gpt-5.5"}`), "", downstreamHeaders)
	if err != nil {
		t.Fatalf("ExecuteOpenAIResponsesCompactRequest() error = %v", err)
	}
	resp.Body.Close()

	for range 2 {
		got := <-results
		if got.lite != "" {
			t.Fatalf("%s forwarded %s = %q for non-lite model, want stripped", got.path, codexResponsesLiteHeader, got.lite)
		}
	}
	if got := downstreamHeaders.Get(codexResponsesLiteHeader); got != "true" {
		t.Fatalf("caller headers were mutated: %q", got)
	}
}

// stubWebsocketExecute 桩掉 WS 执行器并捕获帧体/握手头，t.Cleanup 自动还原。
func stubWebsocketExecute(t *testing.T, capturedBody *[]byte, capturedHeaders *http.Header) {
	t.Helper()
	previousExec := WebsocketExecuteFunc
	t.Cleanup(func() { WebsocketExecuteFunc = previousExec })
	WebsocketExecuteFunc = func(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string, apiKey string, deviceCfg *DeviceProfileConfig, headers http.Header, poolRouteKey string) (*http.Response, error) {
		*capturedBody = append([]byte(nil), requestBody...)
		if headers != nil {
			*capturedHeaders = headers.Clone()
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	}
}

// 锁死 ExecuteRequest 的门禁位置（headline 修复路径，issue「gpt-5.5 + lite 必 400」）：
// 下游带 lite 信号、模型已被入口映射改写成已知非 lite 模型时，WS 帧体不得携带
// 标记、HTTP 请求不得携带 lite 头。若门禁被移到 payload 规则改写之前或从任一
// 传输分支上掉线，本测试失败。
func TestExecuteRequestGateStripsLiteOnBothTransports(t *testing.T) {
	resetLearnedResponsesLiteSupport(t)

	downstream := make(http.Header)
	downstream.Set(codexResponsesLiteHeader, "true")
	account := &auth.Account{DBID: 9, AccessToken: "at-9", AccountID: "acct-9"}

	// WS 路径
	var wsBody []byte
	wsHeaders := make(http.Header)
	stubWebsocketExecute(t, &wsBody, &wsHeaders)
	resp, err := ExecuteRequest(context.Background(), account, []byte(`{"model":"gpt-5.5","input":"hi"}`), "", "", "sk-test", nil, downstream, true)
	if err != nil {
		t.Fatalf("ExecuteRequest(ws) error = %v", err)
	}
	resp.Body.Close()
	if marker := gjson.GetBytes(wsBody, codexResponsesLiteWSMetadataPath); marker.Exists() {
		t.Fatalf("WS frame retained lite marker for non-lite model: %s", wsBody)
	}
	if got := wsHeaders.Get(codexResponsesLiteHeader); got != "" {
		t.Fatalf("WS handshake headers carried %s = %q, want stripped", codexResponsesLiteHeader, got)
	}

	// HTTP 路径（Resin 反代指向测试服务器）
	liteHeader := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liteHeader <- r.Header.Get(codexResponsesLiteHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test"}`))
	}))
	t.Cleanup(server.Close)
	SetResinConfig(&ResinConfig{BaseURL: server.URL, PlatformName: "test"})
	t.Cleanup(func() { SetResinConfig(nil) })

	resp, err = ExecuteRequest(context.Background(), account, []byte(`{"model":"gpt-5.5","input":"hi"}`), "", "", "sk-test", nil, downstream, false)
	if err != nil {
		t.Fatalf("ExecuteRequest(http) error = %v", err)
	}
	resp.Body.Close()
	if got := <-liteHeader; got != "" {
		t.Fatalf("HTTP upstream received %s = %q for non-lite model, want stripped", codexResponsesLiteHeader, got)
	}
}

// 锁死 lite 签名在 payload 规则改写之后采集：规则给 lite 能力模型注入的 WS 标记
// 必须到达上游帧体。若签名回退到规则前采集，注入会被 prepare 的清理分支静默删除。
func TestExecuteRequestPayloadRuleInjectedLiteMarkerSurvives(t *testing.T) {
	resetLearnedResponsesLiteSupport(t)
	withPayloadRules(t, `{"override":[{"models":["gpt-5.6-sol"],"params":{"client_metadata.ws_request_header_x_openai_internal_codex_responses_lite":"true"}}]}`)

	var wsBody []byte
	wsHeaders := make(http.Header)
	stubWebsocketExecute(t, &wsBody, &wsHeaders)

	account := &auth.Account{DBID: 9, AccessToken: "at-9", AccountID: "acct-9"}
	resp, err := ExecuteRequest(context.Background(), account, []byte(`{"model":"gpt-5.6-sol","input":"hi"}`), "", "", "sk-test", nil, http.Header{}, true)
	if err != nil {
		t.Fatalf("ExecuteRequest(ws) error = %v", err)
	}
	resp.Body.Close()
	if got := gjson.GetBytes(wsBody, codexResponsesLiteWSMetadataPath).String(); got != "true" {
		t.Fatalf("rule-injected lite marker lost on WS frame: %s", wsBody)
	}
}
