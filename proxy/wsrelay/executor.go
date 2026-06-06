package wsrelay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ==================== WebSocket 执行器常量 ====================

const (
	// Beta header 用于启用 WebSocket 响应 API
	responsesWebsocketBetaHeader = "responses_websockets=2026-02-06"

	// Codex WebSocket 端点
	CodexWsEndpoint = "/responses"
)

func shouldSendWebsocketUserAgent() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CODEX_WS_SEND_USER_AGENT"))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// ==================== WebSocket 执行器 ====================

// Executor WebSocket 执行器
type Executor struct {
	manager *Manager
	mu      sync.RWMutex
}

// NewExecutor 创建 WebSocket 执行器
func NewExecutor() *Executor {
	return &Executor{
		manager: GetManager(),
	}
}

// NewExecutorWithManager 创建带指定管理器的执行器
func NewExecutorWithManager(manager *Manager) *Executor {
	return &Executor{
		manager: manager,
	}
}

// ExecuteRequestViaWebsocket 通过 WebSocket 发送请求
func (e *Executor) ExecuteRequestViaWebsocket(
	ctx context.Context,
	account *auth.Account,
	requestBody []byte,
	sessionID string,
	proxyOverride string,
	apiKey string,
	deviceCfg *proxy.DeviceProfileConfig,
	ginHeaders http.Header,
) (*WsResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	account.Mu().RLock()
	accessToken := account.AccessToken
	accountIDStr := account.AccountID
	account.Mu().RUnlock()

	if accessToken == "" {
		return nil, fmt.Errorf("无可用 access_token")
	}

	// 准备请求体
	wsBody := e.prepareWebsocketBody(requestBody, sessionID)

	// 构建 WebSocket URL
	httpURL := proxy.CodexBaseURL + CodexWsEndpoint
	wsURL, err := buildWebsocketURL(httpURL)
	if err != nil {
		return nil, fmt.Errorf("构建 WebSocket URL 失败: %w", err)
	}

	// Resin 反向代理：改写 WS URL 为 Resin 反代地址
	if proxy.IsResinEnabled() {
		wsURL = proxy.BuildWebSocketURL(wsURL)
	}

	// 准备请求头
	headers := e.prepareWebsocketHeaders(accessToken, accountIDStr, sessionID, apiKey, deviceCfg, ginHeaders)

	// Resin 反代：注入账号身份头
	if proxy.IsResinEnabled() {
		headers.Set("X-Resin-Account", proxy.ResinAccountID(account))
	}

	// 获取或创建连接
	wc, pr, err := e.manager.AcquireConnection(ctx, account, wsURL, sessionID, headers, proxyOverride)
	if err != nil {
		return nil, err
	}

	// 发送请求，失败时最多重试 2 次（重建连接）
	sendErr := e.sendRequest(wc, wsBody, pr.RequestID)
	for retries := 0; sendErr != nil && retries < 2; retries++ {
		wc.session.RemovePendingRequest(pr.RequestID)
		e.manager.RemoveConnection(account.ID(), wsURL, sessionID, proxyOverride)

		// 短暂退避，避免瞬间重连风暴
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(retries+1) * 200 * time.Millisecond):
		}

		wc, pr, err = e.manager.AcquireConnection(ctx, account, wsURL, sessionID, headers, proxyOverride)
		if err != nil {
			return nil, err
		}
		sendErr = e.sendRequest(wc, wsBody, pr.RequestID)
	}
	if sendErr != nil {
		wc.session.RemovePendingRequest(pr.RequestID)
		e.manager.ReleaseConnection(wc)
		return nil, fmt.Errorf("发送 WebSocket 请求失败: %w", sendErr)
	}

	// 启动心跳
	e.manager.StartHeartbeat(wc)

	return &WsResponse{
		conn:        wc,
		pendingReq:  pr,
		sessionID:   sessionID,
		manager:     e.manager,
		readErrChan: make(chan error, 1),
	}, nil
}

// prepareWebsocketBody 准备 WebSocket 请求体
func (e *Executor) prepareWebsocketBody(body []byte, sessionID string) []byte {
	if len(body) == 0 {
		return nil
	}

	// 克隆并修改请求体
	wsBody := bytes.Clone(body)

	// 1. 确保 instructions 字段存在
	if !gjson.GetBytes(wsBody, "instructions").Exists() {
		wsBody, _ = sjson.SetBytes(wsBody, "instructions", "")
	}

	// 2. 清理多余字段（prompt_cache_retention 上游不接受，会返回 400 Unsupported parameter，必须删除）
	wsBody, _ = sjson.DeleteBytes(wsBody, "prompt_cache_retention")
	wsBody, _ = sjson.DeleteBytes(wsBody, "safety_identifier")
	wsBody, _ = sjson.DeleteBytes(wsBody, "disable_response_storage")

	// 3. 注入 prompt_cache_key
	existingCacheKey := strings.TrimSpace(gjson.GetBytes(wsBody, "prompt_cache_key").String())
	if sessionID != "" {
		wsBody, _ = sjson.SetBytes(wsBody, "prompt_cache_key", sessionID)
	} else if existingCacheKey != "" {
		wsBody, _ = sjson.SetBytes(wsBody, "prompt_cache_key", existingCacheKey)
	}

	// 4. 设置请求类型和 stream
	wsBody, _ = sjson.SetBytes(wsBody, "type", "response.create")
	wsBody, _ = sjson.SetBytes(wsBody, "stream", true)

	return wsBody
}

// prepareWebsocketHeaders 准备 WebSocket 请求头
func (e *Executor) prepareWebsocketHeaders(accessToken, accountID, sessionID, apiKey string, deviceCfg *proxy.DeviceProfileConfig, ginHeaders http.Header) http.Header {
	headers := http.Header{}

	// 认证头
	headers.Set("Authorization", "Bearer "+accessToken)

	// Beta header 启用 WebSocket 响应 API
	headers.Set("OpenAI-Beta", responsesWebsocketBetaHeader)

	if shouldSendWebsocketUserAgent() {
		account := &auth.Account{}
		if accountID != "" {
			account.AccountID = accountID
			if id, err := strconv.ParseInt(accountID, 10, 64); err == nil {
				account.DBID = id
			}
		}
		if proxy.IsDeviceProfileStabilizationEnabled(deviceCfg) {
			profile := proxy.ResolveDeviceProfile(account, apiKey, ginHeaders, deviceCfg)
			headers.Set("User-Agent", profile.UserAgent)
			if version := strings.TrimSpace(profile.PackageVersion); version != "" {
				headers.Set("Version", version)
			}
		} else if userAgent := strings.TrimSpace(ginHeaders.Get("User-Agent")); proxy.IsCodexOfficialClientByHeaders(userAgent, ginHeaders.Get("Originator")) && userAgent != "" {
			headers.Set("User-Agent", userAgent)
			if version := strings.TrimSpace(ginHeaders.Get("Version")); version != "" {
				headers.Set("Version", version)
			}
		} else {
			headers.Set("User-Agent", proxy.MinimalCodexCLIUserAgentForHeaders())
			headers.Set("Version", proxy.LatestCodexCLIVersionForHeaders())
		}
	}
	if betaFeatures := strings.TrimSpace(ginHeaders.Get("X-Codex-Beta-Features")); betaFeatures != "" {
		headers.Set("X-Codex-Beta-Features", betaFeatures)
	} else if deviceCfg != nil && strings.TrimSpace(deviceCfg.BetaFeatures) != "" {
		headers.Set("X-Codex-Beta-Features", strings.TrimSpace(deviceCfg.BetaFeatures))
	}

	// Originator
	if originator := strings.TrimSpace(ginHeaders.Get("Originator")); originator != "" && proxy.IsCodexOfficialClientByHeaders("", originator) {
		headers.Set("Originator", originator)
	} else {
		headers.Set("Originator", proxy.Originator)
	}
	for _, name := range []string{"X-Codex-Turn-State", "X-Codex-Turn-Metadata", "X-Client-Request-Id", "X-Responsesapi-Include-Timing-Metrics"} {
		if value := strings.TrimSpace(ginHeaders.Get(name)); value != "" {
			headers.Set(name, value)
		}
	}

	// Account ID
	if accountID != "" {
		headers.Set("Chatgpt-Account-Id", accountID)
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		headers.Set("Session_id", sessionID)
		headers.Set("Conversation_id", sessionID)
	}

	return headers
}

// sendRequest 发送 WebSocket 请求
func (e *Executor) sendRequest(wc *WsConnection, body []byte, requestID string) error {
	if !wc.IsConnected() {
		return fmt.Errorf("websocket connection is not connected")
	}
	return wc.WriteMessage(websocket.TextMessage, body)
}

// ==================== WebSocket 响应处理 ====================

// WsResponse WebSocket 响应包装器
type WsResponse struct {
	conn        *WsConnection
	pendingReq  *PendingRequest
	sessionID   string
	manager     *Manager
	readErrChan chan error
	closed      bool
	mu          sync.Mutex
}

// ReadStream 读取 SSE 流
func (r *WsResponse) ReadStream(callback func(data []byte) bool) error {
	if r.conn == nil || !r.conn.IsConnected() {
		return fmt.Errorf("websocket connection is not available")
	}

	for {
		msgType, payload, err := r.conn.ReadMessage()
		if err != nil {
			// 检查是否是正常关闭
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("websocket read error: %w", err)
		}

		// 只处理文本消息
		if msgType != websocket.TextMessage {
			if msgType == websocket.BinaryMessage {
				return fmt.Errorf("unexpected binary message from websocket")
			}
			continue
		}

		// 清理消息
		payload = bytes.TrimSpace(payload)
		if len(payload) == 0 {
			continue
		}

		// 解析并处理消息
		if err := r.handleMessage(payload, callback); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// handleMessage 处理单条 WebSocket 消息
func (r *WsResponse) handleMessage(payload []byte, callback func(data []byte) bool) error {
	// 上游错误帧：透传给下游(转成 SSE 错误事件)，而不是转成 Go error 后静默关闭 pipe。
	// 否则下游只会读到一个底层 read error → 表现为空响应，无从得知具体错误。
	if errEvent, isErr := r.buildErrorEvent(payload); isErr {
		// 把错误内容作为 SSE 数据写给下游，让客户端看到完整错误 JSON。
		callback(errEvent)
		// 错误即终止：结束流(等价于 response.failed)。
		return io.EOF
	}

	// 标准化完成事件类型
	payload = normalizeCompletionEvent(payload)

	// 调用回调
	if !callback(payload) {
		return io.EOF
	}

	// 检查是否是终止事件
	eventType := gjson.GetBytes(payload, "type").String()
	if eventType == "response.completed" || eventType == "response.failed" {
		return io.EOF
	}

	return nil
}

// buildErrorEvent 判断 payload 是否为上游错误帧；若是，返回一个下游可识别的
// response.failed SSE 事件(保留原始错误内容)，第二个返回值标记是否为错误帧。
func (r *WsResponse) buildErrorEvent(payload []byte) ([]byte, bool) {
	if len(payload) == 0 {
		return nil, false
	}
	if gjson.GetBytes(payload, "type").String() != "error" {
		return nil, false
	}

	status := int(gjson.GetBytes(payload, "status").Int())
	if status == 0 {
		status = int(gjson.GetBytes(payload, "status_code").Int())
	}

	errMsg := gjson.GetBytes(payload, "error.message").String()
	if errMsg == "" {
		errMsg = gjson.GetBytes(payload, "message").String()
	}
	if errMsg == "" && status > 0 {
		errMsg = http.StatusText(status)
	}
	if errMsg == "" {
		errMsg = "upstream websocket error"
	}

	// 构造 response.failed 事件：下游 ReadSSEStream 已识别该类型为终止失败，
	// 与 HTTP 路径的错误语义对齐；同时保留原始上游错误对象供客户端排查。
	errObj := gjson.GetBytes(payload, "error").Raw
	if errObj == "" {
		errObj = fmt.Sprintf(`{"message":%q,"code":%d}`, errMsg, status)
	}
	event := fmt.Sprintf(`{"type":"response.failed","response":{"status":"failed","error":%s}}`, errObj)
	return []byte(event), true
}

// normalizeCompletionEvent 标准化完成事件类型
func normalizeCompletionEvent(payload []byte) []byte {
	if gjson.GetBytes(payload, "type").String() == "response.done" {
		updated, err := sjson.SetBytes(payload, "type", "response.completed")
		if err == nil && len(updated) > 0 {
			return updated
		}
	}
	return payload
}

// Close 关闭响应并归还连接
func (r *WsResponse) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	r.closed = true

	// 移除等待请求
	if r.conn != nil && r.conn.session != nil {
		r.conn.session.RemovePendingRequest(r.pendingReq.RequestID)
	}

	// 归还连接至连接池
	if r.conn != nil {
		r.manager.ReleaseConnection(r.conn)
	}

	return nil
}

// HTTPResponse 返回 HTTP 握手响应
func (r *WsResponse) HTTPResponse() *http.Response {
	if r.conn != nil {
		return r.conn.HTTPResponse()
	}
	return nil
}

// ==================== 辅助函数 ====================

// buildWebsocketURL 从 HTTP URL 构建 WebSocket URL
func buildWebsocketURL(httpURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(httpURL))
	if err != nil {
		return "", err
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	}

	return parsed.String(), nil
}

// ==================== 全局执行器实例 ====================

var globalExecutor *Executor
var executorOnce sync.Once

// GetExecutor 获取全局执行器实例
func GetExecutor() *Executor {
	executorOnce.Do(func() {
		globalExecutor = NewExecutor()
	})
	return globalExecutor
}

// ShutdownExecutor 关闭全局执行器和管理器
func ShutdownExecutor() {
	ShutdownManager()
}

// ExecuteRequestWebsocket 通过 WebSocket 发送请求
// 返回一个模拟的 http.Response 用于兼容现有代码
func ExecuteRequestWebsocket(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string, apiKey string, deviceCfg *proxy.DeviceProfileConfig, headers http.Header) (*http.Response, error) {
	exec := GetExecutor()
	wsResp, err := exec.ExecuteRequestViaWebsocket(ctx, account, requestBody, sessionID, proxyOverride, apiKey, deviceCfg, headers)
	if err != nil {
		return nil, err
	}

	// 检查 HTTP 握手响应状态。WebSocket 握手成功的标准状态是 101，
	// 但这里要包装成现有 handler 可消费的 SSE HTTP 200 响应。
	statusCode, handshakeHeader, handshakeFailed := normalizeWebsocketHandshakeResponse(wsResp.HTTPResponse())
	if handshakeFailed {
		wsResp.Close()
		return &http.Response{
			StatusCode: statusCode,
			Header:     handshakeHeader.Clone(),
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf("websocket handshake failed: %d", statusCode))),
		}, nil
	}

	return websocketResponseToHTTP(ctx, wsResp, statusCode, handshakeHeader), nil
}

func websocketResponseToHTTP(ctx context.Context, wsResp *WsResponse, statusCode int, handshakeHeader http.Header) *http.Response {
	if ctx == nil {
		ctx = context.Background()
	}

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       pr,
	}

	// 从 HTTP 握手响应中复制头信息
	if handshakeHeader != nil {
		for key, values := range handshakeHeader {
			for _, v := range values {
				resp.Header.Add(key, v)
			}
		}
	}

	// 设置 SSE 响应头
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Header.Set("Cache-Control", "no-cache")
	resp.Header.Set("Connection", "keep-alive")

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = wsResp.Close()
			_ = pw.CloseWithError(ctx.Err())
		case <-done:
		}
	}()

	// 在后台读取 WebSocket 流并写入 pipe
	go func() {
		defer close(done)
		defer pw.Close()
		defer wsResp.Close()

		err := wsResp.ReadStream(func(data []byte) bool {
			// 将数据编码为 SSE 格式
			line := fmt.Sprintf("data: %s\n\n", string(data))
			if _, err := pw.Write([]byte(line)); err != nil {
				return false
			}
			return true
		})

		if err != nil && err != io.EOF {
			pw.CloseWithError(err)
		}
	}()

	return resp
}

func normalizeWebsocketHandshakeResponse(handshakeResp *http.Response) (statusCode int, header http.Header, failed bool) {
	if handshakeResp == nil {
		return http.StatusOK, http.Header{}, false
	}

	statusCode = handshakeResp.StatusCode
	header = handshakeResp.Header
	if statusCode == http.StatusSwitchingProtocols || (statusCode >= 200 && statusCode < 300) {
		return http.StatusOK, header, false
	}
	return statusCode, header, true
}
