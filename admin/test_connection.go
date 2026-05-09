package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// testEvent SSE 测试事件
type testEvent struct {
	Type    string `json:"type"`              // test_start | content | test_complete | error
	Text    string `json:"text,omitempty"`    // 内容文本
	Model   string `json:"model,omitempty"`   // 测试模型
	Success bool   `json:"success,omitempty"` // 是否成功
	Error   string `json:"error,omitempty"`   // 错误信息
}

// TestConnection 测试账号连接（SSE 流式返回）
// GET /api/admin/accounts/:id/test
func (h *Handler) TestConnection(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的账号 ID"})
		return
	}

	// 查找运行时账号
	account := h.store.FindByID(id)
	if account == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不在运行时池中"})
		return
	}

	// 检查 access_token 是否可用
	account.Mu().RLock()
	hasToken := account.AccessToken != ""
	account.Mu().RUnlock()

	if !hasToken {
		c.JSON(http.StatusBadRequest, gin.H{"error": "账号没有可用的 Access Token，请先刷新"})
		return
	}

	testModel := strings.TrimSpace(c.Query("model"))
	if testModel == "" {
		testModel = h.connectionTestModel(c.Request.Context())
	} else if !proxy.IsTextTestModelID(c.Request.Context(), h.db, testModel) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的测试模型: " + testModel})
		return
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()

	// 发送 test_start
	sendTestEvent(c, testEvent{Type: "test_start", Model: testModel})

	// 构建最小测试请求体（参考 sub2api createOpenAITestPayload）
	payload := buildTestPayload(testModel)

	// 发送请求
	start := time.Now()
	resp, reqErr := proxy.ExecuteRequest(c.Request.Context(), account, payload, "", h.store.ResolveProxyForAccount(account), "", nil, nil)
	if reqErr != nil {
		sendTestEvent(c, testEvent{Type: "error", Error: fmt.Sprintf("请求失败: %s", reqErr.Error())})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		proxy.SyncCodexUsageState(h.store, account, resp)
		errBody, _ := io.ReadAll(resp.Body)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			h.store.MarkCooldown(account, 24*time.Hour, "unauthorized")
		case http.StatusTooManyRequests:
			proxy.Apply429Cooldown(h.store, account, errBody, resp, testModel)
		}
		sendTestEvent(c, testEvent{Type: "error", Error: fmt.Sprintf("上游返回 %d: %s", resp.StatusCode, truncate(string(errBody), 500))})
		return
	}

	usageState := proxy.SyncCodexUsageState(h.store, account, resp)

	// 解析 SSE 流
	hasContent := false
	gotTerminal := false
	sentTerminal := false
	var lastUpstreamEvent []byte
	readErr := proxy.ReadSSEStream(resp.Body, func(data []byte) bool {
		lastUpstreamEvent = append(lastUpstreamEvent[:0], data...)
		eventType := gjson.GetBytes(data, "type").String()

		switch eventType {
		case "response.output_text.delta":
			delta := gjson.GetBytes(data, "delta").String()
			if delta != "" {
				hasContent = true
				sendTestEvent(c, testEvent{Type: "content", Text: delta})
			}
		case "response.output_text.done":
			if !hasContent {
				text := gjson.GetBytes(data, "text").String()
				if text != "" {
					hasContent = true
					sendTestEvent(c, testEvent{Type: "content", Text: text})
				}
			}
		case "response.content_part.done":
			if !hasContent {
				text := gjson.GetBytes(data, "part.text").String()
				if text != "" {
					hasContent = true
					sendTestEvent(c, testEvent{Type: "content", Text: text})
				}
			}
		case "response.output_item.done":
			if !hasContent {
				text := extractOutputItemText(gjson.GetBytes(data, "item"))
				if text != "" {
					hasContent = true
					sendTestEvent(c, testEvent{Type: "content", Text: text})
				}
			}
		case "response.completed":
			gotTerminal = true
			if status := gjson.GetBytes(data, "response.status").String(); status == "failed" || status == "incomplete" {
				sentTerminal = true
				sendTestEvent(c, testEvent{Type: "error", Error: formatUpstreamTestError(data, "上游返回 "+status)})
				return false
			}
			if !hasContent {
				text := extractCompletedOutputText(data)
				if text != "" {
					hasContent = true
					sendTestEvent(c, testEvent{Type: "content", Text: text})
				}
			}
			if !hasContent {
				sentTerminal = true
				sendTestEvent(c, testEvent{Type: "error", Error: formatNoOutputUpstreamError(data)})
				return false
			}
			// 测试成功即重置冷却状态，用量限制由调度器自行判断
			if !usageState.Premium5hRateLimited && (!usageState.HasUsage7d || usageState.UsagePct7d < 100) {
				h.store.ClearCooldown(account)
			}
			// 如果上游未返回用量头，清除旧的用量缓存，避免显示过期数据
			if !usageState.HasUsage7d && !usageState.HasUsage5h {
				account.ClearUsageCache()
			}
			duration := time.Since(start).Milliseconds()
			sendTestEvent(c, testEvent{
				Type: "content",
				Text: fmt.Sprintf("\n\n--- 耗时 %dms ---", duration),
			})
			sendTestEvent(c, testEvent{Type: "test_complete", Success: true})
			sentTerminal = true
			return false
		case "response.failed":
			gotTerminal = true
			sentTerminal = true
			sendTestEvent(c, testEvent{Type: "error", Error: formatUpstreamTestError(data, "上游返回 response.failed")})
			return false
		case "error":
			gotTerminal = true
			sentTerminal = true
			sendTestEvent(c, testEvent{Type: "error", Error: formatUpstreamTestError(data, "上游返回 error 事件")})
			return false
		}
		return true
	})

	if readErr != nil && !sentTerminal {
		sendTestEvent(c, testEvent{Type: "error", Error: "读取上游流失败: " + readErr.Error()})
		return
	}
	if !gotTerminal && !sentTerminal {
		sendTestEvent(c, testEvent{Type: "error", Error: formatMissingTerminalUpstreamError(lastUpstreamEvent)})
	}
}

// buildTestPayload 构建最小测试请求体
func buildTestPayload(model string) []byte {
	payload := []byte(`{}`)
	payload, _ = sjson.SetBytes(payload, "model", model)
	payload, _ = sjson.SetBytes(payload, "input", []map[string]any{
		{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "input_text",
					"text": "Say hello in one sentence.",
				},
			},
		},
	})
	payload, _ = sjson.SetBytes(payload, "stream", true)
	payload, _ = sjson.SetBytes(payload, "store", false)
	payload, _ = sjson.SetBytes(payload, "instructions", "You are a helpful assistant. Reply briefly.")
	return payload
}

// sendTestEvent 发送 SSE 事件
func sendTestEvent(c *gin.Context, event testEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("序列化测试事件失败: %v", err)
		return
	}
	if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
		log.Printf("写入 SSE 事件失败: %v", err)
		return
	}
	c.Writer.Flush()
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func extractCompletedOutputText(data []byte) string {
	if text := gjson.GetBytes(data, "response.output_text").String(); text != "" {
		return text
	}
	return extractOutputItemText(gjson.GetBytes(data, "response"))
}

func extractOutputItemText(item gjson.Result) string {
	var b strings.Builder
	writeTextFromOutputItem(&b, item)
	return b.String()
}

func writeTextFromOutputItem(b *strings.Builder, item gjson.Result) {
	if !item.Exists() {
		return
	}
	switch item.Get("type").String() {
	case "output_text", "text":
		b.WriteString(item.Get("text").String())
	case "message", "assistant":
		writeTextFromContentArray(b, item.Get("content"))
	default:
		if output := item.Get("output"); output.IsArray() {
			output.ForEach(func(_, child gjson.Result) bool {
				writeTextFromOutputItem(b, child)
				return true
			})
		}
		writeTextFromContentArray(b, item.Get("content"))
	}
}

func writeTextFromContentArray(b *strings.Builder, content gjson.Result) {
	if !content.IsArray() {
		return
	}
	content.ForEach(func(_, part gjson.Result) bool {
		partType := part.Get("type").String()
		if partType == "output_text" || partType == "text" {
			b.WriteString(part.Get("text").String())
		}
		return true
	})
}

func formatUpstreamTestError(data []byte, fallback string) string {
	msg := firstNonEmptyGJSONString(data,
		"response.status_details.error.message",
		"response.error.message",
		"error.message",
		"message",
		"response.incomplete_details.reason",
		"response.status_details.message",
	)
	if msg == "" {
		msg = fallback
	}

	code := firstNonEmptyGJSONString(data,
		"response.status_details.error.code",
		"response.error.code",
		"error.code",
	)
	if code != "" && !strings.Contains(msg, code) {
		msg += " (code: " + code + ")"
	}

	return formatUpstreamEventDetail(msg, data)
}

func formatNoOutputUpstreamError(data []byte) string {
	msg := "上游已完成但没有返回文本输出"
	if status := gjson.GetBytes(data, "response.status").String(); status != "" && status != "completed" {
		msg = "上游响应状态: " + status
	}
	if reason := gjson.GetBytes(data, "response.incomplete_details.reason").String(); reason != "" {
		msg += " (" + reason + ")"
	}
	return formatUpstreamEventDetail(msg, data)
}

func formatMissingTerminalUpstreamError(lastEvent []byte) string {
	if len(lastEvent) == 0 {
		return "上游流结束但未收到任何事件"
	}
	return formatUpstreamEventDetail("上游流提前结束，未收到 response.completed 或 response.failed", lastEvent)
}

func firstNonEmptyGJSONString(data []byte, paths ...string) string {
	for _, path := range paths {
		if value := strings.TrimSpace(gjson.GetBytes(data, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func formatUpstreamEventDetail(message string, data []byte) string {
	if len(data) == 0 {
		return message
	}
	detail := string(data)
	var parsed any
	if err := json.Unmarshal(data, &parsed); err == nil {
		if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
			detail = string(pretty)
		}
	}
	return message + "\n\n上游事件:\n" + truncate(detail, 3000)
}

func isSupportedConnectionTestModel(model string) bool {
	if strings.Contains(strings.ToLower(model), "image") {
		return false
	}
	for _, supported := range proxy.SupportedModels {
		if model == supported {
			return true
		}
	}
	return false
}

func (h *Handler) connectionTestModel(ctx context.Context) string {
	model := strings.TrimSpace(h.store.GetTestModel())
	if proxy.IsTextTestModelID(ctx, h.db, model) {
		return model
	}
	models := proxy.TextTestModelIDs(ctx, h.db)
	if len(models) > 0 {
		return models[0]
	}
	return "gpt-5.4"
}

type batchTestRequest struct {
	IDs *[]int64 `json:"ids"`
}

func resolveBatchTestAccounts(store *auth.Store, ids *[]int64) ([]*auth.Account, int) {
	if store == nil {
		return nil, 0
	}
	if ids == nil {
		return store.Accounts(), 0
	}

	accounts := make([]*auth.Account, 0, len(*ids))
	missing := 0
	seen := make(map[int64]struct{}, len(*ids))
	for _, id := range *ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		acc := store.FindByID(id)
		if acc == nil {
			missing++
			continue
		}
		accounts = append(accounts, acc)
	}
	return accounts, missing
}

// BatchTest 批量测试账号连接；未传 ids 时测试所有账号，传 ids 时仅测试指定账号。
// POST /api/admin/accounts/batch-test
func (h *Handler) BatchTest(c *gin.Context) {
	var req batchTestRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "请求格式错误")
			return
		}
	}
	if req.IDs != nil && len(*req.IDs) == 0 {
		writeError(c, http.StatusBadRequest, "请提供要测试的账号 ID 列表")
		return
	}

	accounts, missingCount := resolveBatchTestAccounts(h.store, req.IDs)
	if len(accounts) == 0 && missingCount == 0 {
		c.JSON(http.StatusOK, gin.H{"total": 0, "success": 0, "failed": 0, "banned": 0, "rate_limited": 0})
		return
	}

	testModel := h.connectionTestModel(c.Request.Context())
	payload := buildTestPayload(testModel)
	concurrency := h.store.GetTestConcurrency()

	var (
		successCount   int64
		failedCount    = int64(missingCount)
		bannedCount    int64
		rateLimitCount int64
		wg             sync.WaitGroup
		sem            = make(chan struct{}, concurrency)
	)

	for _, account := range accounts {
		// 跳过没有 token 的账号
		account.Mu().RLock()
		hasToken := account.AccessToken != ""
		hasRefreshToken := account.RefreshToken != ""
		account.Mu().RUnlock()
		if !hasToken {
			if !hasRefreshToken {
				h.store.MarkError(account, "批量测试失败: 账号缺少 access_token 和 refresh_token")
			}
			atomic.AddInt64(&failedCount, 1)
			continue
		}

		wg.Add(1)
		go func(acc *auth.Account) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resp, err := proxy.ExecuteRequest(context.Background(), acc, payload, "", h.store.ResolveProxyForAccount(acc), "", nil, nil)
			if err != nil {
				h.store.MarkError(acc, "批量测试请求失败: "+err.Error())
				atomic.AddInt64(&failedCount, 1)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			switch resp.StatusCode {
			case http.StatusOK:
				usageState := proxy.SyncCodexUsageState(h.store, acc, resp)
				// 测试成功即重置冷却状态，用量限制由调度器自行判断
				if !usageState.Premium5hRateLimited && (!usageState.HasUsage7d || usageState.UsagePct7d < 100) {
					h.store.ClearCooldown(acc)
				}
				atomic.AddInt64(&successCount, 1)
			case http.StatusUnauthorized:
				proxy.SyncCodexUsageState(h.store, acc, resp)
				h.store.MarkCooldown(acc, 24*time.Hour, "unauthorized")
				atomic.AddInt64(&bannedCount, 1)
			case http.StatusTooManyRequests:
				proxy.SyncCodexUsageState(h.store, acc, resp)
				proxy.Apply429Cooldown(h.store, acc, body, resp, testModel)
				atomic.AddInt64(&rateLimitCount, 1)
			default:
				if shouldMarkBatchTestAccountError(resp.StatusCode, body) {
					h.store.MarkError(acc, fmt.Sprintf("批量测试上游返回 %d: %s", resp.StatusCode, truncate(string(body), 300)))
				}
				atomic.AddInt64(&failedCount, 1)
			}
		}(account)
	}

	wg.Wait()

	c.JSON(http.StatusOK, gin.H{
		"total":        len(accounts) + missingCount,
		"success":      successCount,
		"failed":       failedCount,
		"banned":       bannedCount,
		"rate_limited": rateLimitCount,
	})
}

func shouldMarkBatchTestAccountError(statusCode int, body []byte) bool {
	msg := strings.ToLower(string(body))
	if statusCode == http.StatusPaymentRequired && proxy.IsDeactivatedWorkspaceError(body) {
		return true
	}
	if statusCode == http.StatusForbidden {
		return true
	}
	if statusCode == http.StatusBadRequest {
		for _, needle := range []string{
			"invalid_grant",
			"invalid_client",
			"unauthorized_client",
			"access_denied",
			"account_deactivated",
			"unsupported_country_region_territory",
		} {
			if strings.Contains(msg, needle) {
				return true
			}
		}
	}
	return false
}
