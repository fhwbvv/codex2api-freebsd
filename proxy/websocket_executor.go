package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codex2api/auth"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ==================== WebSocket 执行器常量 ====================

const (
	// Beta header 用于启用 WebSocket 响应 API
	responsesWebsocketBetaHeader = "responses_websockets=2026-02-06"

	// WebSocket 空闲超时（5分钟）
	websocketIdleTimeout = 5 * time.Minute

	// WebSocket 握手超时
	websocketHandshakeTimeout = 30 * time.Second

	// Ping/Pong 心跳间隔
	websocketPingInterval = 30 * time.Second
)

// ==================== WebSocket 连接池 ====================

// websocketConn 包装 WebSocket 连接，添加连接管理功能
type websocketConn struct {
	conn       *websocket.Conn
	accountID  int64
	wsURL      string
	lastUsed   atomic.Int64 // UnixNano 时间戳
	closed     atomic.Bool
	mu         sync.Mutex // 保护所有写操作
	inUse      atomic.Bool // 连接是否被租用
}

// touch 更新最后使用时间
func (wc *websocketConn) touch() {
	wc.lastUsed.Store(time.Now().UnixNano())
}

// isExpired 检查连接是否已过期（空闲超时）
func (wc *websocketConn) isExpired() bool {
	lastUsed := time.Unix(0, wc.lastUsed.Load())
	return time.Since(lastUsed) > websocketIdleTimeout
}

// close 安全关闭连接
func (wc *websocketConn) close() error {
	if wc.closed.CompareAndSwap(false, true) {
		return wc.conn.Close()
	}
	return nil
}

// websocketConnPool WebSocket 连接池管理器
type websocketConnPool struct {
	connections sync.Map // map[string]*websocketConn, key = accountID|wsURL
	ticker      *time.Ticker
	stopCh      chan struct{}
}

// newWebsocketConnPool 创建新的 WebSocket 连接池
func newWebsocketConnPool() *websocketConnPool {
	pool := &websocketConnPool{
		ticker: time.NewTicker(30 * time.Second),
		stopCh: make(chan struct{}),
	}

	// 启动后台清理协程
	go pool.cleanupLoop()

	return pool
}

// cleanupLoop 定期清理过期连接
func (p *websocketConnPool) cleanupLoop() {
	for {
		select {
		case <-p.ticker.C:
			p.evictExpiredConnections()
		case <-p.stopCh:
			p.ticker.Stop()
			return
		}
	}
}

// evictExpiredConnections 清理过期连接
func (p *websocketConnPool) evictExpiredConnections() {
	p.connections.Range(func(key, value any) bool {
		wc := value.(*websocketConn)
		if wc.isExpired() || wc.closed.Load() {
			p.connections.Delete(key)
			wc.close()
		}
		return true
	})
}

// stop 停止连接池清理协程
func (p *websocketConnPool) stop() {
	close(p.stopCh)
}

// getPoolKey 生成连接池键
func getPoolKey(accountID int64, wsURL string) string {
	return fmt.Sprintf("%d|%s", accountID, wsURL)
}

// acquireConn 从连接池获取连接并标记为租用
func (p *websocketConnPool) acquireConn(
	ctx context.Context,
	account *auth.Account,
	wsURL string,
	headers http.Header,
	dialer *websocket.Dialer,
) (*websocketConn, *http.Response, error) {
	key := getPoolKey(account.ID(), wsURL)

	// 尝试获取现有连接
	if v, ok := p.connections.Load(key); ok {
		wc := v.(*websocketConn)
		if !wc.closed.Load() && !wc.isExpired() {
			if wc.inUse.CompareAndSwap(false, true) {
				wc.touch()
				return wc, nil, nil
			}
			// 连接已被租用，继续尝试创建新连接
		}
		// 连接已关闭或过期，删除旧连接
		p.connections.Delete(key)
		wc.close()
	}

	// 创建新连接
	account.Mu().RLock()
	accessToken := account.AccessToken
	account.Mu().RUnlock()

	if accessToken == "" {
		return nil, nil, fmt.Errorf("无可用 access_token")
	}

	// 设置握手超时
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = websocketHandshakeTimeout
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return nil, resp, fmt.Errorf("WebSocket 握手失败: %w", err)
	}

	wc := &websocketConn{
		conn:      conn,
		accountID: account.ID(),
		wsURL:     wsURL,
	}
	wc.touch()
	wc.inUse.Store(true)

	// PongHandler 用于处理 Pong 消息，更新最后活跃时间（不加锁，仅操作原子变量）
	conn.SetPongHandler(func(appData string) error {
		wc.touch()
		return nil
	})

	// 启动心跳协程
	go p.heartbeat(wc)

	// 存储新连接
	if v, loaded := p.connections.LoadOrStore(key, wc); loaded {
		// 已有其他协程创建了连接，关闭新连接并返回已存在的
		wc.inUse.Store(false)
		wc.close()
		existing := v.(*websocketConn)
		if existing.inUse.CompareAndSwap(false, true) {
			existing.touch()
			return existing, resp, nil
		}
		// 已有连接也被占用了，创建一个新的专用连接
		return wc, resp, nil
	}

	return wc, resp, nil
}

// releaseConn 归还连接至连接池
func (p *websocketConnPool) releaseConn(wc *websocketConn) {
	if wc == nil {
		return
	}
	wc.inUse.Store(false)
	wc.touch()
}

// heartbeat 发送 Ping 心跳保持连接活跃
func (p *websocketConnPool) heartbeat(wc *websocketConn) {
	ticker := time.NewTicker(websocketPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if wc.closed.Load() {
				return
			}

			// 发送 Ping（加锁保护写操作）
			wc.mu.Lock()
			deadline := time.Now().Add(10 * time.Second)
			err := wc.conn.WriteControl(websocket.PingMessage, []byte{}, deadline)
			wc.mu.Unlock()

			if err != nil {
				// Ping 失败，标记连接为关闭
				wc.close()
				key := getPoolKey(wc.accountID, wc.wsURL)
				p.connections.Delete(key)
				return
			}
		}
	}
}

// removeConn 从连接池中移除连接
func (p *websocketConnPool) removeConn(accountID int64, wsURL string) {
	key := getPoolKey(accountID, wsURL)
	if v, ok := p.connections.LoadAndDelete(key); ok {
		wc := v.(*websocketConn)
		wc.close()
	}
}

// closeAllConnections 关闭所有连接
func (p *websocketConnPool) closeAllConnections() {
	p.connections.Range(func(key, value any) bool {
		wc := value.(*websocketConn)
		p.connections.Delete(key)
		wc.close()
		return true
	})
}

// 全局 WebSocket 连接池实例
var wsConnPool = newWebsocketConnPool()

// ==================== WebSocket 执行器 ====================

// WebsocketExecutor WebSocket 执行器
type WebsocketExecutor struct {
	mu sync.RWMutex
}

// NewWebsocketExecutor 创建新的 WebSocket 执行器
func NewWebsocketExecutor() *WebsocketExecutor {
	return &WebsocketExecutor{}
}

// ExecuteRequestViaWebsocket 通过 WebSocket 发送请求
func (e *WebsocketExecutor) ExecuteRequestViaWebsocket(
	ctx context.Context,
	account *auth.Account,
	requestBody []byte,
	sessionID string,
	proxyOverride string,
) (*WebsocketResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	account.Mu().RLock()
	accessToken := account.AccessToken
	accountID := account.AccountID
	proxyURL := account.ProxyURL
	account.Mu().RUnlock()

	// 代理池优先级: proxyOverride > account.ProxyURL
	if proxyOverride != "" {
		proxyURL = proxyOverride
	}

	if accessToken == "" {
		return nil, fmt.Errorf("无可用 access_token")
	}

	// 准备请求体
	wsBody := e.prepareWebsocketBody(requestBody, sessionID)

	// 构建 WebSocket URL
	httpURL := CodexBaseURL + "/responses"
	wsURL, err := buildWebsocketURL(httpURL)
	if err != nil {
		return nil, fmt.Errorf("构建 WebSocket URL 失败: %w", err)
	}

	// 准备请求头
	headers := e.prepareWebsocketHeaders(accessToken, accountID)

	// 创建 WebSocket 拨号器
	dialer := e.createWebsocketDialer(proxyURL)

	// 获取或创建连接
	wc, resp, err := wsConnPool.acquireConn(ctx, account, wsURL, headers, dialer)
	if err != nil {
		return nil, err
	}

	// 发送请求
	if err := e.sendRequest(wc, wsBody); err != nil {
		// 发送失败，移除连接并重试一次
		wsConnPool.removeConn(account.ID(), wsURL)
		wc.inUse.Store(false)

		wc, resp, err = wsConnPool.acquireConn(ctx, account, wsURL, headers, dialer)
		if err != nil {
			return nil, err
		}

		if err := e.sendRequest(wc, wsBody); err != nil {
			wsConnPool.releaseConn(wc)
			return nil, fmt.Errorf("发送 WebSocket 请求失败: %w", err)
		}
	}

	return &WebsocketResponse{
		conn:     wc,
		sessionID: sessionID,
		httpResp: resp,
	}, nil
}

// prepareWebsocketBody 准备 WebSocket 请求体
func (e *WebsocketExecutor) prepareWebsocketBody(body []byte, sessionID string) []byte {
	if len(body) == 0 {
		return nil
	}

	// 克隆并修改请求体
	wsBody := bytes.Clone(body)

	// 1. 确保 instructions 字段存在
	if !gjson.GetBytes(wsBody, "instructions").Exists() {
		wsBody, _ = sjson.SetBytes(wsBody, "instructions", "")
	}

	// 2. 清理多余字段
	wsBody, _ = sjson.DeleteBytes(wsBody, "previous_response_id")
	wsBody, _ = sjson.DeleteBytes(wsBody, "prompt_cache_retention")
	wsBody, _ = sjson.DeleteBytes(wsBody, "safety_identifier")
	wsBody, _ = sjson.DeleteBytes(wsBody, "disable_response_storage")

	// 3. 注入 prompt_cache_key
	existingCacheKey := strings.TrimSpace(gjson.GetBytes(wsBody, "prompt_cache_key").String())
	if existingCacheKey == "" && sessionID != "" {
		wsBody, _ = sjson.SetBytes(wsBody, "prompt_cache_key", sessionID)
	}

	// 4. 设置请求类型为 response.create
	wsBody, _ = sjson.SetBytes(wsBody, "type", "response.create")
	wsBody, _ = sjson.SetBytes(wsBody, "stream", true)

	return wsBody
}

// prepareWebsocketHeaders 准备 WebSocket 请求头
func (e *WebsocketExecutor) prepareWebsocketHeaders(accessToken, accountID string) http.Header {
	headers := http.Header{}

	// 认证头
	headers.Set("Authorization", "Bearer "+accessToken)

	// Beta header 启用 WebSocket 响应 API
	headers.Set("OpenAI-Beta", responsesWebsocketBetaHeader)

	// Session ID
	headers.Set("session_id", uuid.New().String())

	// User-Agent 和版本 - 根据账号 ID 确定性地选择 profile
	var accountIDInt int64
	if accountID != "" {
		// 尝试解析账号 ID
		if id, err := strconv.ParseInt(accountID, 10, 64); err == nil {
			accountIDInt = id
		}
	}
	profile := ProfileForAccount(accountIDInt)
	headers.Set("User-Agent", profile.UserAgent)
	headers.Set("Version", profile.Version)

	// Originator
	headers.Set("Originator", Originator)

	// Account ID
	if accountID != "" {
		headers.Set("Chatgpt-Account-Id", accountID)
	}

	return headers
}

// createWebsocketDialer 创建 WebSocket 拨号器
func (e *WebsocketExecutor) createWebsocketDialer(proxyURL string) *websocket.Dialer {
	dialer := &websocket.Dialer{
		HandshakeTimeout:  websocketHandshakeTimeout,
		EnableCompression: true,
		NetDialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	// 配置代理
	if proxyURL != "" {
		dialer.Proxy = func(req *http.Request) (*url.URL, error) {
			return url.Parse(proxyURL)
		}
	}

	return dialer
}

// sendRequest 发送 WebSocket 请求
func (e *WebsocketExecutor) sendRequest(wc *websocketConn, body []byte) error {
	if wc.closed.Load() {
		return fmt.Errorf("websocket connection is closed")
	}

	wc.mu.Lock()
	defer wc.mu.Unlock()

	// 设置写超时
	wc.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	defer wc.conn.SetWriteDeadline(time.Time{})

	return wc.conn.WriteMessage(websocket.TextMessage, body)
}

// ==================== WebSocket 响应处理 ====================

// WebsocketResponse WebSocket 响应包装器
type WebsocketResponse struct {
	conn      *websocketConn
	sessionID string
	httpResp  *http.Response
	mu        sync.Mutex
	closed    bool
}

// ReadStream 读取 SSE 流
func (r *WebsocketResponse) ReadStream(callback func(data []byte) bool) error {
	if r.conn == nil || r.conn.closed.Load() {
		return fmt.Errorf("websocket connection is nil or closed")
	}

	for {
		// 设置读超时
		r.conn.conn.SetReadDeadline(time.Now().Add(websocketIdleTimeout))

		msgType, payload, err := r.conn.conn.ReadMessage()
		if err != nil {
			// 检查是否是正常关闭
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("websocket read error: %w", err)
		}

		// 更新最后使用时间
		r.conn.touch()

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
			return err
		}
	}
}

// handleMessage 处理单条 WebSocket 消息
func (r *WebsocketResponse) handleMessage(payload []byte, callback func(data []byte) bool) error {
	// 检查是否是错误消息
	if err := r.checkError(payload); err != nil {
		return err
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

// checkError 检查并返回 WebSocket 错误
func (r *WebsocketResponse) checkError(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}

	// 检查错误类型
	if gjson.GetBytes(payload, "type").String() != "error" {
		return nil
	}

	status := int(gjson.GetBytes(payload, "status").Int())
	if status == 0 {
		status = int(gjson.GetBytes(payload, "status_code").Int())
	}
	if status <= 0 {
		return nil
	}

	// 构建错误消息
	errMsg := gjson.GetBytes(payload, "error.message").String()
	if errMsg == "" {
		errMsg = http.StatusText(status)
	}

	return fmt.Errorf("websocket error (status %d): %s", status, errMsg)
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

// Close 关闭响应并归还连接至连接池
func (r *WebsocketResponse) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	r.closed = true

	// 归还连接至连接池（而非销毁）
	if r.conn != nil {
		wsConnPool.releaseConn(r.conn)
	}

	return nil
}

// HTTPResponse 返回 HTTP 握手响应
func (r *WebsocketResponse) HTTPResponse() *http.Response {
	return r.httpResp
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

// InitWebsocketExecutor 初始化 WebSocket 执行器
func InitWebsocketExecutor() *WebsocketExecutor {
	return NewWebsocketExecutor()
}

// ShutdownWebsocketPool 关闭 WebSocket 连接池
func ShutdownWebsocketPool() {
	if wsConnPool != nil {
		wsConnPool.closeAllConnections()
		wsConnPool.stop()
	}
}
