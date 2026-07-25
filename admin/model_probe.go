package admin

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// 单模型探测结果分类。
const (
	modelProbeAvailable   = "available"   // 200 完成且有输出：账号确认可用该模型
	modelProbeUnsupported = "unsupported" // 上游明确拒绝：账号套餐不支持该模型
	modelProbeThrottled   = "throttled"   // 429/用量耗尽：模型可能可用但当前被限流，结果不可靠
	modelProbeError       = "error"       // 其他错误：超时/传输/鉴权/5xx 等，无法判定
)

// 单模型探测并发上限，避免一次点击对同一账号打出过多并发请求。
const modelProbeMaxConcurrency = 6

type modelProbeResult struct {
	Model   string `json:"model"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

// modelProbeEvent 是探测过程推送给前端的 SSE 事件。
// type: start（下发全部待测模型）| testing（某模型开始探测）| result（某模型出结果）| done（结束，含可用集合）
type modelProbeEvent struct {
	Type      string   `json:"type"`
	Total     int      `json:"total,omitempty"`
	Current   int      `json:"current,omitempty"`
	Models    []string `json:"models,omitempty"`
	Model     string   `json:"model,omitempty"`
	Outcome   string   `json:"outcome,omitempty"`
	Detail    string   `json:"detail,omitempty"`
	Available []string `json:"available,omitempty"`
}

// ProbeAccountModels 用账号自身凭据并发探测系统模型列表（已排除 image 模型），
// 判定每个模型是否可用。全程只读，不回写账号调度状态（冷却/错误/成功）。
// stream=true 时以 SSE 逐模型推送进度，否则一次性返回 JSON。
// POST /api/admin/accounts/:id/models/probe
func (h *Handler) ProbeAccountModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的账号 ID")
		return
	}
	account := h.store.FindByID(id)
	if account == nil {
		writeError(c, http.StatusNotFound, "账号不在运行时池中")
		return
	}
	if account.IsRelayStyle() {
		writeError(c, http.StatusBadRequest, "中转/Grok 账号不支持模型探测")
		return
	}
	if !account.IsCodexAgentIdentity() && account.GetAccessToken() == "" {
		writeError(c, http.StatusBadRequest, "账号没有可用的 Access Token，请先刷新")
		return
	}

	models := proxy.TextTestModelIDs(c.Request.Context(), h.db)
	streaming := strings.EqualFold(c.Query("stream"), "true")

	if len(models) == 0 {
		if streaming {
			setupSSE(c)
			sendSSEJSON(c, modelProbeEvent{Type: "start", Total: 0, Models: []string{}})
			sendSSEJSON(c, modelProbeEvent{Type: "done", Available: []string{}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"available": []string{}, "results": []modelProbeResult{}})
		return
	}

	concurrency := h.store.GetTestConcurrency()
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > modelProbeMaxConcurrency {
		concurrency = modelProbeMaxConcurrency
	}

	if streaming {
		h.streamProbeModels(c, account, models, concurrency)
		return
	}

	results := h.runProbeModels(c.Request.Context(), account, models, concurrency, nil)
	available := collectAvailableModels(results)
	c.JSON(http.StatusOK, gin.H{
		"available": available,
		"results":   results,
	})
}

// streamProbeModels 以 SSE 逐模型推送探测进度。
func (h *Handler) streamProbeModels(c *gin.Context, account *auth.Account, models []string, concurrency int) {
	setupSSE(c)
	sendSSEJSON(c, modelProbeEvent{Type: "start", Total: len(models), Models: models})

	events := make(chan modelProbeEvent, len(models)+1)
	ctx := c.Request.Context()
	go func() {
		results := h.runProbeModels(ctx, account, models, concurrency, func(ev modelProbeEvent) {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		})
		select {
		case events <- modelProbeEvent{Type: "done", Available: collectAvailableModels(results)}:
		case <-ctx.Done():
		}
		close(events)
	}()

	for ev := range events {
		sendSSEJSON(c, ev)
	}
}

// runProbeModels 并发探测所有模型；onEvent 非空时逐模型回调 testing/result 事件（用于 SSE）。
func (h *Handler) runProbeModels(ctx context.Context, account *auth.Account, models []string, concurrency int, onEvent func(modelProbeEvent)) []modelProbeResult {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		results   = make([]modelProbeResult, len(models))
		sem       = make(chan struct{}, concurrency)
		completed int
		total     = len(models)
	)
	for i, model := range models {
		wg.Add(1)
		go func(idx int, m string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				results[idx] = modelProbeResult{Model: m, Outcome: modelProbeError, Detail: "探测已取消"}
				completed++
				current := completed
				mu.Unlock()
				if onEvent != nil {
					onEvent(modelProbeEvent{Type: "result", Model: m, Outcome: modelProbeError, Detail: "探测已取消", Current: current, Total: total})
				}
				return
			}
			defer func() { <-sem }()

			if onEvent != nil {
				onEvent(modelProbeEvent{Type: "testing", Model: m})
			}
			outcome, detail := h.probeAccountModel(ctx, account, m)
			mu.Lock()
			results[idx] = modelProbeResult{Model: m, Outcome: outcome, Detail: detail}
			completed++
			current := completed
			mu.Unlock()
			if onEvent != nil {
				onEvent(modelProbeEvent{Type: "result", Model: m, Outcome: outcome, Detail: detail, Current: current, Total: total})
			}
		}(i, model)
	}
	wg.Wait()
	return results
}

func collectAvailableModels(results []modelProbeResult) []string {
	available := make([]string, 0, len(results))
	for _, r := range results {
		if r.Outcome == modelProbeAvailable {
			available = append(available, r.Model)
		}
	}
	available = auth.NormalizeAccountModels(available)
	sort.Strings(available)
	return available
}

// probeAccountModel 对单个模型发起最小探测请求并分类结果。不回写任何账号状态。
func (h *Handler) probeAccountModel(ctx context.Context, account *auth.Account, model string) (string, string) {
	probeCtx, cancel := context.WithTimeout(ctx, batchTestAccountTimeout)
	defer cancel()

	payload := buildConnectionTestPayload(h.store, model)
	resp, err := proxy.ExecuteRequest(probeCtx, account, payload, "", h.store.ResolveProxyForAccount(account), "", nil, nil)
	if err != nil {
		if msg, ok := batchTestContextFailure(probeCtx, err); ok {
			return modelProbeError, msg
		}
		return modelProbeError, err.Error()
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return readProbeStream(probeCtx, resp)
	case http.StatusTooManyRequests:
		return modelProbeThrottled, "上游返回 429 限流"
	case http.StatusBadRequest:
		body, _ := readBatchTestErrorBody(probeCtx, resp.Body)
		if proxy.IsCodexModelUnsupportedError(body) {
			return modelProbeUnsupported, "账号套餐不支持该模型"
		}
		return modelProbeError, fmt.Sprintf("上游返回 400: %s", truncate(string(body), 200))
	case http.StatusUnauthorized:
		return modelProbeError, "账号授权失败（401）"
	default:
		body, _ := readBatchTestErrorBody(probeCtx, resp.Body)
		return modelProbeError, fmt.Sprintf("上游返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
}

// readProbeStream 读取探测 SSE 流并分类，能从终止事件里识别出"账号不支持该模型"。
// 不回写任何账号状态。
func readProbeStream(ctx context.Context, resp *http.Response) (string, string) {
	hasContent := false
	gotTerminal := false
	outcome := ""
	detail := ""
	var lastUpstreamEvent []byte

	classifyFailure := func(data []byte, fallback string) (string, string) {
		if proxy.IsUsageLimitReachedError(data) {
			return modelProbeThrottled, formatUpstreamTestError(data, "上游用量耗尽")
		}
		if proxy.IsCodexModelUnsupportedError(data) {
			return modelProbeUnsupported, "账号套餐不支持该模型"
		}
		return modelProbeError, formatUpstreamTestError(data, fallback)
	}

	readErr := proxy.ReadSSEStream(resp.Body, func(data []byte) bool {
		lastUpstreamEvent = append(lastUpstreamEvent[:0], data...)
		switch gjson.GetBytes(data, "type").String() {
		case "response.output_text.delta":
			if gjson.GetBytes(data, "delta").String() != "" {
				hasContent = true
			}
		case "response.output_text.done":
			if !hasContent && gjson.GetBytes(data, "text").String() != "" {
				hasContent = true
			}
		case "response.content_part.done":
			if !hasContent && gjson.GetBytes(data, "part.text").String() != "" {
				hasContent = true
			}
		case "response.output_item.done":
			if !hasContent && extractOutputItemText(gjson.GetBytes(data, "item")) != "" {
				hasContent = true
			}
		case "response.completed":
			gotTerminal = true
			if status := gjson.GetBytes(data, "response.status").String(); status == "failed" || status == "incomplete" {
				outcome, detail = classifyFailure(data, "上游返回 "+status)
				return false
			}
			if !hasContent && extractCompletedOutputText(data) != "" {
				hasContent = true
			}
			if !hasContent {
				outcome = modelProbeError
				detail = formatNoOutputUpstreamError(data)
				return false
			}
			outcome = modelProbeAvailable
			detail = "探测通过"
			return false
		case "response.failed":
			gotTerminal = true
			outcome, detail = classifyFailure(data, "上游返回 response.failed")
			return false
		case "error":
			gotTerminal = true
			outcome, detail = classifyFailure(data, "上游返回 error 事件")
			return false
		}
		return true
	})

	if readErr != nil {
		if msg, ok := batchTestContextFailure(ctx, readErr); ok {
			return modelProbeError, msg
		}
		return modelProbeError, readErr.Error()
	}
	if outcome != "" {
		return outcome, detail
	}
	if !gotTerminal {
		return modelProbeError, formatMissingTerminalUpstreamError(lastUpstreamEvent)
	}
	return modelProbeError, "上游探测未返回明确结果"
}
