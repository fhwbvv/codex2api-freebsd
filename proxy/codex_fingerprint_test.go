package proxy

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/codex2api/auth"
)

func fingerprintAccount(t *testing.T, mode string) *auth.Account {
	t.Helper()
	return &auth.Account{DBID: 42, CodexFingerprintMode: mode}
}

// codexClientHeaders 模拟真实 Codex CLI 的下游请求头：带 x-codex- 前缀头（引擎特征）
// 和连字符形式的 session-id。
func codexClientHeaders(turnMetadata, sessionID string) http.Header {
	headers := http.Header{}
	if turnMetadata != "" {
		headers.Set("X-Codex-Turn-Metadata", turnMetadata)
	}
	if sessionID != "" {
		headers.Set("Session-Id", sessionID)
	}
	return headers
}

func TestDeriveStableCodexUUIDIsDeterministicAndV4(t *testing.T) {
	first := deriveStableCodexUUID("seed-a")
	if second := deriveStableCodexUUID("seed-a"); first != second {
		t.Fatalf("deriveStableCodexUUID(%q) = %q then %q, want identical values", "seed-a", first, second)
	}
	if other := deriveStableCodexUUID("seed-b"); other == first {
		t.Fatalf("different seeds produced the same value %q", first)
	}

	parsed, err := uuid.Parse(first)
	if err != nil {
		t.Fatalf("uuid.Parse(%q) error = %v", first, err)
	}
	if parsed.Version() != 4 {
		t.Fatalf("UUID version = %d, want 4 (must match the client's v4 identifiers)", parsed.Version())
	}
	if parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("UUID variant = %v, want RFC4122", parsed.Variant())
	}
}

func TestResolveCodexFingerprintIDsPerMode(t *testing.T) {
	headers := codexClientHeaders("", "client-session-1")

	if ids := resolveCodexFingerprintIDs(fingerprintAccount(t, auth.CodexFingerprintModeOff), headers); ids != nil {
		t.Fatalf("off mode returned %#v, want nil", ids)
	}
	if ids := resolveCodexFingerprintIDs(nil, headers); ids != nil {
		t.Fatalf("nil account returned %#v, want nil", ids)
	}

	device := resolveCodexFingerprintIDs(fingerprintAccount(t, auth.CodexFingerprintModeDevice), headers)
	if device == nil || device.installationID == "" {
		t.Fatalf("device mode ids = %#v, want a converged installation id", device)
	}
	if device.sessionID != "" || device.threadID != "" || device.windowID != "" {
		t.Fatalf("device mode converged session/thread/window (%#v), want installation id only", device)
	}

	session := resolveCodexFingerprintIDs(fingerprintAccount(t, auth.CodexFingerprintModeSession), headers)
	if session == nil {
		t.Fatal("session mode returned nil")
	}
	if session.installationID != device.installationID {
		t.Fatalf("installation id = %q, want %q (must not depend on mode)", session.installationID, device.installationID)
	}
	if session.sessionID == "" || session.threadID == "" {
		t.Fatalf("session mode ids = %#v, want session and thread ids", session)
	}
	if session.threadID == session.sessionID {
		t.Fatal("session mode thread id equals session id, want a thread derived from the client session")
	}
	if want := session.threadID + ":0"; session.windowID != want {
		t.Fatalf("window id = %q, want %q", session.windowID, want)
	}

	// 不同客户端会话派生出不同线程，同一客户端会话恒定派生同一线程。
	other := resolveCodexFingerprintIDs(fingerprintAccount(t, auth.CodexFingerprintModeSession), codexClientHeaders("", "client-session-2"))
	if other.threadID == session.threadID {
		t.Fatal("distinct client sessions derived the same thread id")
	}
	if other.sessionID != session.sessionID {
		t.Fatalf("session id = %q, want %q (account level constant)", other.sessionID, session.sessionID)
	}
	repeat := resolveCodexFingerprintIDs(fingerprintAccount(t, auth.CodexFingerprintModeSession), headers)
	if repeat.threadID != session.threadID {
		t.Fatalf("thread id = %q, want %q (same client session must be stable)", repeat.threadID, session.threadID)
	}

	full := resolveCodexFingerprintIDs(fingerprintAccount(t, auth.CodexFingerprintModeFull), headers)
	if full.threadID != full.sessionID {
		t.Fatalf("full mode thread id = %q, want it to equal session id %q", full.threadID, full.sessionID)
	}

	// 客户端没发会话头时，session 档退回单线程而不是每请求漂移。
	noSession := resolveCodexFingerprintIDs(fingerprintAccount(t, auth.CodexFingerprintModeSession), http.Header{})
	if noSession.threadID != noSession.sessionID {
		t.Fatalf("thread id = %q, want fallback to session id %q", noSession.threadID, noSession.sessionID)
	}
}

func TestResolveCodexFingerprintIDsIsolatesAccounts(t *testing.T) {
	headers := codexClientHeaders("", "client-session-1")
	first := resolveCodexFingerprintIDs(&auth.Account{DBID: 1, CodexFingerprintMode: auth.CodexFingerprintModeSession}, headers)
	second := resolveCodexFingerprintIDs(&auth.Account{DBID: 2, CodexFingerprintMode: auth.CodexFingerprintModeSession}, headers)
	if first.installationID == second.installationID {
		t.Fatal("distinct accounts share an installation id")
	}
	if first.sessionID == second.sessionID {
		t.Fatal("distinct accounts share a session id")
	}
	if first.threadID == second.threadID {
		t.Fatal("distinct accounts share a thread id")
	}
}

// TestResolveCodexFingerprintIDsHonorsPinnedInstallationHeader 验证运维在账号自定义头里
// 钉死的 installation id 优先于派生值——自定义头本来就会覆盖出站头，取同一值才能让
// 请求头与 metadata 一致。
func TestResolveCodexFingerprintIDsHonorsPinnedInstallationHeader(t *testing.T) {
	account := &auth.Account{
		DBID:                 42,
		CodexFingerprintMode: auth.CodexFingerprintModeDevice,
		CustomHeaders:        map[string]string{"x-codex-installation-id": "pinned-device-id"},
	}
	ids := resolveCodexFingerprintIDs(account, http.Header{})
	if ids.installationID != "pinned-device-id" {
		t.Fatalf("installation id = %q, want the pinned custom header value", ids.installationID)
	}
}

func TestApplyCodexFingerprintHeadersRewritesOnlyPresentTurnMetadataKeys(t *testing.T) {
	// sandbox / thread_source 等非身份字段必须原样保留，缺失的 window_id 不能被补上。
	const raw = `{"thread_id":"client-thread","installation_id":"client-install","sandbox":"danger-full-access","session_id":"client-session","turn_id":"client-turn","turn_started_at_unix_ms":1730000000000}`
	outbound := http.Header{}
	outbound.Set("X-Codex-Turn-Metadata", raw)
	account := fingerprintAccount(t, auth.CodexFingerprintModeSession)
	downstream := codexClientHeaders(raw, "client-session")

	ApplyCodexFingerprintHeaders(outbound, account, downstream)

	got := outbound.Get("X-Codex-Turn-Metadata")
	ids := resolveCodexFingerprintIDs(account, downstream)
	if value := gjson.Get(got, "installation_id").String(); value != ids.installationID {
		t.Fatalf("installation_id = %q, want %q", value, ids.installationID)
	}
	if value := gjson.Get(got, "session_id").String(); value != ids.sessionID {
		t.Fatalf("session_id = %q, want %q", value, ids.sessionID)
	}
	if value := gjson.Get(got, "thread_id").String(); value != ids.threadID {
		t.Fatalf("thread_id = %q, want %q", value, ids.threadID)
	}
	if gjson.Get(got, "window_id").Exists() {
		t.Fatalf("window_id was added to %q, want absent keys left absent", got)
	}
	if value := gjson.Get(got, "sandbox").String(); value != "danger-full-access" {
		t.Fatalf("sandbox = %q, want it preserved", value)
	}
	// turn_id 与其时间戳是逐轮随机值，不标识设备；重写只会引入头体不一致。
	if value := gjson.Get(got, "turn_id").String(); value != "client-turn" {
		t.Fatalf("turn_id = %q, want the client value preserved", value)
	}
	if value := gjson.Get(got, "turn_started_at_unix_ms").Int(); value != 1730000000000 {
		t.Fatalf("turn_started_at_unix_ms = %d, want the client value preserved", value)
	}
	// 键序必须保持原样：重排本身就是可识别特征。
	if wantPrefix := `{"thread_id":"`; !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("rewritten metadata = %q, want original key order starting with %q", got, wantPrefix)
	}
}

func TestApplyCodexFingerprintHeadersDeviceModeLeavesSessionFieldsAlone(t *testing.T) {
	const raw = `{"installation_id":"client-install","session_id":"client-session","thread_id":"client-thread"}`
	outbound := http.Header{}
	outbound.Set("X-Codex-Turn-Metadata", raw)

	ApplyCodexFingerprintHeaders(outbound, fingerprintAccount(t, auth.CodexFingerprintModeDevice), codexClientHeaders(raw, "client-session"))

	got := outbound.Get("X-Codex-Turn-Metadata")
	if value := gjson.Get(got, "installation_id").String(); value == "client-install" {
		t.Fatal("installation_id was not converged in device mode")
	}
	if value := gjson.Get(got, "session_id").String(); value != "client-session" {
		t.Fatalf("session_id = %q, want the client value untouched in device mode", value)
	}
	if value := gjson.Get(got, "thread_id").String(); value != "client-thread" {
		t.Fatalf("thread_id = %q, want the client value untouched in device mode", value)
	}
	if outbound.Get("X-Codex-Window-Id") != "" {
		t.Fatal("device mode set a window id header")
	}
}

// TestApplyCodexFingerprintHeadersNeverTouchesSessionIDHeader 锁定与既有上游会话
// 隔离逻辑的边界：Session_id 由 resolveUpstreamSessionID 决定，收敛不得介入，
// 否则 prompt cache 隔离语义会被改变。
func TestApplyCodexFingerprintHeadersNeverTouchesSessionIDHeader(t *testing.T) {
	outbound := http.Header{}
	outbound.Set("Session_id", "upstream-isolated-id")
	outbound.Set("X-Codex-Turn-Metadata", `{"session_id":"client-session"}`)

	ApplyCodexFingerprintHeaders(outbound, fingerprintAccount(t, auth.CodexFingerprintModeFull), codexClientHeaders(`{"session_id":"client-session"}`, "client-session"))

	if got := outbound.Get("Session_id"); got != "upstream-isolated-id" {
		t.Fatalf("Session_id = %q, want it left to resolveUpstreamSessionID", got)
	}
	if got := outbound.Get("Thread-Id"); got != "" {
		t.Fatalf("Thread-Id = %q, want unset (not part of this project's outbound header set)", got)
	}
}

// TestApplyCodexFingerprintHeadersSkipsNonCodexClients 验证不给没有 Codex 引擎特征的
// 下游请求伪造 x-codex-* 头——半套特征比不发更可疑。
func TestApplyCodexFingerprintHeadersSkipsNonCodexClients(t *testing.T) {
	outbound := http.Header{}
	plainSDKHeaders := http.Header{"User-Agent": []string{"OpenAI/Python 1.0"}}

	ApplyCodexFingerprintHeaders(outbound, fingerprintAccount(t, auth.CodexFingerprintModeSession), plainSDKHeaders)

	if got := outbound.Get("X-Codex-Installation-Id"); got != "" {
		t.Fatalf("X-Codex-Installation-Id = %q, want unset for a non-Codex downstream", got)
	}
	if got := outbound.Get("X-Codex-Window-Id"); got != "" {
		t.Fatalf("X-Codex-Window-Id = %q, want unset for a non-Codex downstream", got)
	}
}

func TestApplyCodexFingerprintHeadersAddsIdentityHeadersForCodexClients(t *testing.T) {
	outbound := http.Header{}
	account := fingerprintAccount(t, auth.CodexFingerprintModeSession)
	downstream := codexClientHeaders(`{"installation_id":"client-install"}`, "client-session")
	outbound.Set("X-Codex-Turn-Metadata", downstream.Get("X-Codex-Turn-Metadata"))

	ApplyCodexFingerprintHeaders(outbound, account, downstream)

	ids := resolveCodexFingerprintIDs(account, downstream)
	if got := outbound.Get("X-Codex-Installation-Id"); got != ids.installationID {
		t.Fatalf("X-Codex-Installation-Id = %q, want %q", got, ids.installationID)
	}
	if got := outbound.Get("X-Codex-Window-Id"); got != ids.windowID {
		t.Fatalf("X-Codex-Window-Id = %q, want %q", got, ids.windowID)
	}
}

func TestApplyCodexFingerprintOffModeIsNoop(t *testing.T) {
	const raw = `{"installation_id":"client-install","session_id":"client-session"}`
	outbound := http.Header{}
	outbound.Set("X-Codex-Turn-Metadata", raw)
	account := fingerprintAccount(t, auth.CodexFingerprintModeOff)
	downstream := codexClientHeaders(raw, "client-session")

	ApplyCodexFingerprintHeaders(outbound, account, downstream)
	if got := outbound.Get("X-Codex-Turn-Metadata"); got != raw {
		t.Fatalf("turn metadata = %q, want %q unchanged", got, raw)
	}
	if got := outbound.Get("X-Codex-Installation-Id"); got != "" {
		t.Fatalf("X-Codex-Installation-Id = %q, want unset", got)
	}

	body := []byte(`{"client_metadata":{"x-codex-installation-id":"client-install"}}`)
	if got := ApplyCodexFingerprintToBody(body, account, downstream); string(got) != string(body) {
		t.Fatalf("body = %s, want %s unchanged", got, body)
	}
}

func TestApplyCodexFingerprintToBodyRewritesClientMetadata(t *testing.T) {
	account := fingerprintAccount(t, auth.CodexFingerprintModeSession)
	downstream := codexClientHeaders("", "client-session")
	ids := resolveCodexFingerprintIDs(account, downstream)
	body := []byte(`{"model":"gpt-5.6-codex","client_metadata":{"x-codex-installation-id":"client-install","session_id":"client-session","thread_id":"client-thread","x-codex-window-id":"client-window:0","x-codex-turn-metadata":"{\"installation_id\":\"client-install\",\"session_id\":\"client-session\",\"sandbox\":\"read-only\"}"}}`)

	got := ApplyCodexFingerprintToBody(body, account, downstream)

	if value := gjson.GetBytes(got, "client_metadata.x-codex-installation-id").String(); value != ids.installationID {
		t.Fatalf("client_metadata.x-codex-installation-id = %q, want %q", value, ids.installationID)
	}
	if value := gjson.GetBytes(got, "client_metadata.session_id").String(); value != ids.sessionID {
		t.Fatalf("client_metadata.session_id = %q, want %q", value, ids.sessionID)
	}
	if value := gjson.GetBytes(got, "client_metadata.thread_id").String(); value != ids.threadID {
		t.Fatalf("client_metadata.thread_id = %q, want %q", value, ids.threadID)
	}
	if value := gjson.GetBytes(got, "client_metadata.x-codex-window-id").String(); value != ids.windowID {
		t.Fatalf("client_metadata.x-codex-window-id = %q, want %q", value, ids.windowID)
	}
	if value := gjson.GetBytes(got, "model").String(); value != "gpt-5.6-codex" {
		t.Fatalf("model = %q, want it preserved", value)
	}

	embedded := gjson.GetBytes(got, "client_metadata.x-codex-turn-metadata").String()
	if value := gjson.Get(embedded, "installation_id").String(); value != ids.installationID {
		t.Fatalf("embedded installation_id = %q, want %q", value, ids.installationID)
	}
	if value := gjson.Get(embedded, "session_id").String(); value != ids.sessionID {
		t.Fatalf("embedded session_id = %q, want %q", value, ids.sessionID)
	}
	if value := gjson.Get(embedded, "sandbox").String(); value != "read-only" {
		t.Fatalf("embedded sandbox = %q, want it preserved", value)
	}
}

func TestApplyCodexFingerprintToBodyLeavesBodiesWithoutClientMetadata(t *testing.T) {
	account := fingerprintAccount(t, auth.CodexFingerprintModeFull)
	downstream := codexClientHeaders("", "client-session")

	body := []byte(`{"model":"gpt-5.6-codex","input":[]}`)
	if got := ApplyCodexFingerprintToBody(body, account, downstream); string(got) != string(body) {
		t.Fatalf("body = %s, want %s unchanged", got, body)
	}

	// client_metadata 存在但不含身份字段时不得新增字段。
	partial := []byte(`{"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}}`)
	if got := ApplyCodexFingerprintToBody(partial, account, downstream); string(got) != string(partial) {
		t.Fatalf("body = %s, want %s unchanged", got, partial)
	}
}

// TestCodexFingerprintHeaderAndBodyAgree 是这套改写的核心不变量：请求头与请求体在
// 两个独立改写点各自推导，必须落到完全相同的一组标识。
func TestCodexFingerprintHeaderAndBodyAgree(t *testing.T) {
	account := fingerprintAccount(t, auth.CodexFingerprintModeSession)
	const raw = `{"installation_id":"client-install","session_id":"client-session","thread_id":"client-thread","window_id":"client-window:0"}`
	downstream := codexClientHeaders(raw, "client-session")

	outbound := http.Header{}
	outbound.Set("X-Codex-Turn-Metadata", raw)
	ApplyCodexFingerprintHeaders(outbound, account, downstream)

	body := ApplyCodexFingerprintToBody(
		[]byte(`{"client_metadata":{"x-codex-installation-id":"client-install","session_id":"client-session","thread_id":"client-thread","x-codex-window-id":"client-window:0"}}`),
		account, downstream,
	)

	headerMetadata := outbound.Get("X-Codex-Turn-Metadata")
	pairs := []struct {
		name       string
		headerPath string
		bodyPath   string
	}{
		{"installation id", "installation_id", "client_metadata.x-codex-installation-id"},
		{"session id", "session_id", "client_metadata.session_id"},
		{"thread id", "thread_id", "client_metadata.thread_id"},
		{"window id", "window_id", "client_metadata.x-codex-window-id"},
	}
	for _, pair := range pairs {
		fromHeader := gjson.Get(headerMetadata, pair.headerPath).String()
		fromBody := gjson.GetBytes(body, pair.bodyPath).String()
		if fromHeader == "" {
			t.Fatalf("%s missing from rewritten header metadata %q", pair.name, headerMetadata)
		}
		if fromHeader != fromBody {
			t.Fatalf("%s: header = %q, body = %q, want identical values", pair.name, fromHeader, fromBody)
		}
	}
	if got := outbound.Get("X-Codex-Installation-Id"); got != gjson.Get(headerMetadata, "installation_id").String() {
		t.Fatalf("X-Codex-Installation-Id = %q, want it to match the metadata installation id", got)
	}
	if got := outbound.Get("X-Codex-Window-Id"); got != gjson.Get(headerMetadata, "window_id").String() {
		t.Fatalf("X-Codex-Window-Id = %q, want it to match the metadata window id", got)
	}
}

// TestApplyCodexFingerprintHeadersIgnoresInvalidTurnMetadata 验证客户端发来的非法 JSON
// 原样透传，不因改写失败而破坏请求。
func TestApplyCodexFingerprintHeadersIgnoresInvalidTurnMetadata(t *testing.T) {
	outbound := http.Header{}
	outbound.Set("X-Codex-Turn-Metadata", "not-json")

	ApplyCodexFingerprintHeaders(outbound, fingerprintAccount(t, auth.CodexFingerprintModeSession), codexClientHeaders("not-json", "client-session"))

	if got := outbound.Get("X-Codex-Turn-Metadata"); got != "not-json" {
		t.Fatalf("turn metadata = %q, want it passed through unchanged", got)
	}
}

// TestApplyCodexFingerprintSkipsRelayAccounts 验证中转型账号不走 Codex 官方出站路径，
// 恒为 off，即使凭据里配了收敛档位。
func TestApplyCodexFingerprintSkipsRelayAccounts(t *testing.T) {
	relay := &auth.Account{
		DBID:                 42,
		UpstreamType:         auth.UpstreamOpenAIResponses,
		BaseURL:              "https://relay.example.com",
		APIKey:               "sk-relay",
		CodexFingerprintMode: auth.CodexFingerprintModeFull,
	}
	if ids := resolveCodexFingerprintIDs(relay, codexClientHeaders("", "client-session")); ids != nil {
		t.Fatalf("relay account ids = %#v, want nil", ids)
	}

	grok := &auth.Account{
		DBID:                 42,
		UpstreamType:         auth.UpstreamGrok,
		APIKey:               "xai-key",
		CodexFingerprintMode: auth.CodexFingerprintModeFull,
	}
	if ids := resolveCodexFingerprintIDs(grok, codexClientHeaders("", "client-session")); ids != nil {
		t.Fatalf("grok account ids = %#v, want nil", ids)
	}

	// 即使下游带着完整的 Codex 特征头打到 Grok 账号上，也不得改写任何载体。
	const raw = `{"installation_id":"client-install","session_id":"client-session"}`
	outbound := http.Header{}
	outbound.Set("X-Codex-Turn-Metadata", raw)
	downstream := codexClientHeaders(raw, "client-session")
	ApplyCodexFingerprintHeaders(outbound, grok, downstream)
	if got := outbound.Get("X-Codex-Turn-Metadata"); got != raw {
		t.Fatalf("grok turn metadata = %q, want %q unchanged", got, raw)
	}
	if got := outbound.Get("X-Codex-Installation-Id"); got != "" {
		t.Fatalf("grok X-Codex-Installation-Id = %q, want unset", got)
	}
	body := []byte(`{"client_metadata":{"x-codex-installation-id":"client-install"}}`)
	if got := ApplyCodexFingerprintToBody(body, grok, downstream); string(got) != string(body) {
		t.Fatalf("grok body = %s, want %s unchanged", got, body)
	}
}
