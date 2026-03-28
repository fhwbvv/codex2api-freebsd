package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/codex2api/auth"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ==================== HTTP 连接池 ====================

// 全局连接池：按账号维度缓存 *http.Client
// 避免同一代理下的多个账号共用同一条 HTTP/2 连接，降低 GOAWAY 连带影响范围。
var clientPool sync.Map // map[string]*http.Client, key = account-scoped pool key

func clientPoolKey(account *auth.Account, proxyURL string) string {
	return fmt.Sprintf("%d|%s", account.ID(), proxyURL)
}

func shouldRecyclePooledClient(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "goaway") ||
		strings.Contains(msg, "enhance_your_calm") ||
		strings.Contains(msg, "too_many_pings") ||
		strings.Contains(msg, "connection is shutting down")
}

func recyclePooledClient(account *auth.Account, proxyURL string) {
	key := clientPoolKey(account, proxyURL)
	if v, ok := clientPool.LoadAndDelete(key); ok {
		v.(*http.Client).CloseIdleConnections()
	}
}

func recyclePooledClientForAccount(account *auth.Account) {
	if account == nil {
		return
	}

	account.Mu().RLock()
	proxyURL := account.ProxyURL
	account.Mu().RUnlock()
	recyclePooledClient(account, proxyURL)
}

// getPooledClient 获取或创建连接池中的 HTTP Client
func getPooledClient(account *auth.Account, proxyURL string) *http.Client {
	key := clientPoolKey(account, proxyURL)
	if v, ok := clientPool.Load(key); ok {
		return v.(*http.Client)
	}

	transport := &http.Transport{
		// 连接池配置
		MaxIdleConns:        32,               // 账号级别池不需要过大的空闲连接
		MaxIdleConnsPerHost: 16,               // 单账号最多只需维持少量空闲连接
		MaxConnsPerHost:     16,               // 降低单连接池过热概率
		IdleConnTimeout:     90 * time.Second, // 空闲连接超时
		TLSHandshakeTimeout: 10 * time.Second, // TLS 握手超时
		// 启用 HTTP/2
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
	}
	baseDialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport.DialContext = baseDialer.DialContext

	// 设置代理
	if proxyURL != "" {
		_ = auth.ConfigureTransportProxy(transport, proxyURL, baseDialer)
	}

	client := &http.Client{
		Transport: transport,
		// 流式响应不能使用 Client.Timeout，否则长时间生成会被整条链路硬切断。
		// 取消控制改由请求 context 和底层连接超时完成。
		Timeout: 0,
	}

	// CAS 存储，确保相同账号池只创建一个 Client
	if v, loaded := clientPool.LoadOrStore(key, client); loaded {
		return v.(*http.Client)
	}
	return client
}

// Codex 上游常量
const (
	CodexBaseURL = "https://chatgpt.com/backend-api/codex"
	UserAgent    = "codex_cli_rs/0.116.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
	Version      = "0.116.0"
	Originator   = "codex_cli_rs"
)

// ExecuteRequest 向 Codex 上游发送请求
// sessionID 可选，用于 prompt cache 会话绑定
func ExecuteRequest(ctx context.Context, account *auth.Account, requestBody []byte, sessionID string, proxyOverride string) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	account.Mu().RLock()
	accessToken := account.AccessToken
	accountID := account.AccountID
	proxyURL := account.ProxyURL
	account.Mu().RUnlock()

	// 代理池优先级: proxyOverride (来自 NextProxy) > account.ProxyURL
	if proxyOverride != "" {
		proxyURL = proxyOverride
	}

	if accessToken == "" {
		return nil, fmt.Errorf("无可用 access_token")
	}

	// ==================== Codex 请求体优化 ====================
	// 参考 CLIProxyAPI/codex_executor.go + sub2api 的实现

	// 1. 确保 instructions 字段存在（Codex 后端要求）
	if !gjson.GetBytes(requestBody, "instructions").Exists() {
		requestBody, _ = sjson.SetBytes(requestBody, "instructions", "")
	}

	// 2. 清理可能导致上游报错的多余字段
	requestBody, _ = sjson.DeleteBytes(requestBody, "previous_response_id")
	requestBody, _ = sjson.DeleteBytes(requestBody, "prompt_cache_retention")
	requestBody, _ = sjson.DeleteBytes(requestBody, "safety_identifier")
	requestBody, _ = sjson.DeleteBytes(requestBody, "disable_response_storage")

	// 3. 注入 prompt_cache_key（如果请求体中没有，且 sessionID 不为空）
	existingCacheKey := strings.TrimSpace(gjson.GetBytes(requestBody, "prompt_cache_key").String())
	cacheKey := existingCacheKey
	if cacheKey == "" && sessionID != "" {
		cacheKey = sessionID
		requestBody, _ = sjson.SetBytes(requestBody, "prompt_cache_key", cacheKey)
	}

	endpoint := CodexBaseURL + "/responses"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// ==================== 请求头（伪装 Codex CLI） ====================
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Version", Version)
	req.Header.Set("Originator", Originator)
	req.Header.Set("Connection", "Keep-Alive")
	if accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}

	// Session/Conversation 头（用于 prompt cache 绑定）
	// 参考 CLIProxyAPI: req.Header.Set("Conversation_id", cache.ID)
	// 参考 sub2api: headers.Set("session_id", sessionResolution.SessionID)
	if cacheKey != "" {
		req.Header.Set("Session_id", cacheKey)
		req.Header.Set("Conversation_id", cacheKey)
	}

	// 获取连接池 HTTP 客户端（账号级隔离，复用 TCP/TLS 连接）
	client := getPooledClient(account, proxyURL)

	resp, err := client.Do(req)
	if err != nil {
		if shouldRecyclePooledClient(err) {
			recyclePooledClient(account, proxyURL)
		}
		return nil, fmt.Errorf("请求上游失败: %w", err)
	}

	return resp, nil
}

// ResolveSessionID 从下游请求提取或生成 session ID
// 优先级（参考 sub2api）：
//  1. Header: session_id
//  2. Header: conversation_id
//  3. Body:   prompt_cache_key
//  4. 基于 Bearer API Key 的确定性 UUID（参考 CLIProxyAPI）
func ResolveSessionID(authHeader string, body []byte) string {
	// 此函数由 handler 调用，将 gin.Context 的 header 传进来

	// 优先从 body 的 prompt_cache_key 提取
	if v := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); v != "" {
		return v
	}

	// 基于下游用户的 API Key 生成确定性 cache key（参考 CLIProxyAPI codex_executor.go:621）
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		return uuid.NewSHA1(uuid.NameSpaceOID, []byte("codex2api:prompt-cache:"+apiKey)).String()
	}

	// 最后兜底：生成随机 UUID
	return uuid.New().String()
}

// ReadSSEStream 从上游 SSE 响应读取事件流
// callback 返回 true 表示继续读取，false 表示停止
func ReadSSEStream(body io.Reader, callback func(data []byte) bool) error {
	buf := make([]byte, 4096)
	var lineBuf []byte
	var dataLines [][]byte

	emitEvent := func() bool {
		if len(dataLines) == 0 {
			return true
		}

		data := bytes.Join(dataLines, []byte("\n"))
		dataLines = dataLines[:0]
		if bytes.Equal(data, []byte("[DONE]")) {
			return false
		}
		return callback(data)
	}

	for {
		n, err := body.Read(buf)
		if n > 0 {
			lineBuf = append(lineBuf, buf[:n]...)

			// 按行处理
			for {
				idx := bytes.IndexByte(lineBuf, '\n')
				if idx < 0 {
					break
				}

				line := bytes.TrimRight(lineBuf[:idx], "\r")
				lineBuf = lineBuf[idx+1:]

				if len(line) == 0 {
					if !emitEvent() {
						return nil
					}
					continue
				}

				if bytes.HasPrefix(line, []byte(":")) {
					continue
				}

				// 解析 SSE data: 前缀，支持标准多行 data 聚合
				if bytes.HasPrefix(line, []byte("data:")) {
					data := bytes.TrimPrefix(line, []byte("data:"))
					data = bytes.TrimPrefix(data, []byte(" "))
					dataLines = append(dataLines, append([]byte(nil), data...))
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				if len(lineBuf) > 0 {
					line := bytes.TrimRight(lineBuf, "\r")
					if bytes.HasPrefix(line, []byte("data:")) {
						data := bytes.TrimPrefix(line, []byte("data:"))
						data = bytes.TrimPrefix(data, []byte(" "))
						dataLines = append(dataLines, append([]byte(nil), data...))
					}
				}
				if !emitEvent() {
					return nil
				}
				return nil
			}
			return err
		}
	}
}
