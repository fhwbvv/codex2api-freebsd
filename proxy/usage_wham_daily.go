package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/codex2api/auth"
)

// WhamDailyUsageURL 是 ChatGPT 后端按天返回工作区用量统计的端点。
// 与 wham/usage 一样零额度成本，但给的是官方结算口径的绝对值（credits 与 token 数），
// 且能按客户端（CLI / Desktop / IDE / 网关流量）与模型拆分。
//
// 上游只保留最近 7 天：查更早的区间返回空数组，因此历史必须落到本地库，
// 否则过期即永久丢失。
const WhamDailyUsageURL = "https://chatgpt.com/backend-api/wham/analytics/daily-workspace-usage-counts"

// WhamDailyUsageRetentionDays 是上游的历史保留天数（含当天）。实测查询更早的
// 区间一律返回空，快照任务按这个窗口回补即可覆盖漏跑的那几轮。
const WhamDailyUsageRetentionDays = 7

// WhamCreditsPerUSD 是官方 credits 与美元的换算比例（1 美元 = 25 credits）。
const WhamCreditsPerUSD = 25.0

// whamDailyUsageURLForTest 允许测试替换默认 URL。生产代码不要赋值。
var whamDailyUsageURLForTest = ""

// SetWhamDailyUsageURLForTest 供测试替换按天用量端点 URL，返回恢复函数。
// 生产代码不要调用。
func SetWhamDailyUsageURLForTest(u string) (restore func()) {
	old := whamDailyUsageURLForTest
	whamDailyUsageURLForTest = u
	return func() { whamDailyUsageURLForTest = old }
}

// WhamDailyUsageResponse 是 daily-workspace-usage-counts 的响应结构。
type WhamDailyUsageResponse struct {
	Data    []WhamDailyUsageDay `json:"data"`
	GroupBy string              `json:"group_by"`
}

// WhamDailyUsageDay 是单个统计周期（group_by=day 时为一天，week 时为周起始日）。
type WhamDailyUsageDay struct {
	Date    string                 `json:"date"`
	Totals  WhamDailyUsageCounts   `json:"totals"`
	Clients []WhamDailyUsageClient `json:"clients"`
	Models  []WhamDailyUsageModel  `json:"models"`
}

// WhamDailyUsageCounts 是一组用量计数。
//
// 当天（未结算）的记录只有 users/threads/turns/credits，四个 token 字段整个缺失，
// 因此全部用指针：区分「上游没给」与「上游给了 0」，避免把未结算的当天写成 0
// 之后再也不回补。
type WhamDailyUsageCounts struct {
	Users   int     `json:"users"`
	Threads int     `json:"threads"`
	Turns   int     `json:"turns"`
	Credits float64 `json:"credits"`

	UncachedTextInputTokens *int64 `json:"uncached_text_input_tokens,omitempty"`
	CachedTextInputTokens   *int64 `json:"cached_text_input_tokens,omitempty"`
	TextOutputTokens        *int64 `json:"text_output_tokens,omitempty"`
	TextTotalTokens         *int64 `json:"text_total_tokens,omitempty"`
}

// Settled 表示这条记录的 token 明细已结算。当天的记录不带 token 字段，
// 隔天再查同一天才会补齐。
func (c WhamDailyUsageCounts) Settled() bool {
	return c.TextTotalTokens != nil
}

// USD 按官方 1 美元 = 25 credits 折算本条记录的成本。
func (c WhamDailyUsageCounts) USD() float64 {
	return c.Credits / WhamCreditsPerUSD
}

// WhamDailyUsageClient 是按客户端入口拆分的用量。client_id 取值实测包括
// CODEX_CLI / CODEX_DESKTOP_APP / CODEX_IDE_VSCODE / CODEX_WORK_DESKTOP /
// CODEX_SERVICE_EXEC / CODEX_SDK_TS，以及没有官方客户端标识的
// CODEX_UNKNOWN_DEFAULT（走本网关的流量落在这一档）。
type WhamDailyUsageClient struct {
	ClientID string `json:"client_id"`
	WhamDailyUsageCounts
}

// WhamDailyUsageModel 是按模型拆分的用量。实测上游在这一层不返回 token 明细，
// credits 也恒为 0，只有 users/threads/turns 有值。
type WhamDailyUsageModel struct {
	Model string `json:"model"`
	WhamDailyUsageCounts
}

// QueryWhamDailyUsage 拉取 [startDate, endDate] 区间的按天用量统计，日期格式
// YYYY-MM-DD。零额度成本，与 QueryWhamUsage 同款请求形态。
// 非 200 时返回 resp（body 未读）供调用方处理。
func QueryWhamDailyUsage(ctx context.Context, account *auth.Account, proxyURL, startDate, endDate string) (*WhamDailyUsageResponse, *http.Response, error) {
	base := WhamDailyUsageURL
	if whamDailyUsageURLForTest != "" {
		base = whamDailyUsageURLForTest
	}
	return queryWhamDailyUsageWithURL(ctx, account, proxyURL, base, startDate, endDate)
}

// WhamDailyUsageWindow 返回覆盖上游全部保留期的日期区间（含今天）。
func WhamDailyUsageWindow(now time.Time) (startDate, endDate string) {
	end := now.UTC()
	start := end.AddDate(0, 0, -(WhamDailyUsageRetentionDays - 1))
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

func queryWhamDailyUsageWithURL(ctx context.Context, account *auth.Account, proxyURL, base, startDate, endDate string) (*WhamDailyUsageResponse, *http.Response, error) {
	if account == nil {
		return nil, nil, fmt.Errorf("account is nil")
	}
	accessToken := account.GetAccessToken()
	if accessToken == "" {
		return nil, nil, fmt.Errorf("account has no access token")
	}
	if startDate == "" || endDate == "" {
		return nil, nil, fmt.Errorf("daily usage requires start_date and end_date")
	}

	query := url.Values{}
	query.Set("start_date", startDate)
	query.Set("end_date", endDate)
	query.Set("group_by", "day")
	query.Set("workspace_user", "true")
	requestURL := base + "?" + query.Encode()

	// resinMaintenanceTarget 经 RequestURI() 重建目标，query 会被保留。
	finalURL, resinClient, viaResin := resinMaintenanceTarget(account, requestURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, finalURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build daily usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", MinimalCodexCLIUserAgentForHeaders())
	req.Header.Set("Originator", Originator)
	// 与 wham 用量查询一致：自定义头覆盖工作区 ID 时，统计必须查覆盖后的空间，
	// 否则拿到的是与实际流量不同的空间。
	if accountID := account.EffectiveAccountID(); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	client := whamHTTPClient(req, account, resinClient, viaResin, proxyURL)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("daily usage request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp, fmt.Errorf("daily usage returned status %d", resp.StatusCode)
	}

	// 7 天 × 含客户端与模型拆分，实测 pro 号 14KB；上限给到 1MB 足够冗余。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if err != nil {
		return nil, resp, fmt.Errorf("read daily usage response: %w", err)
	}

	var out WhamDailyUsageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, resp, fmt.Errorf("parse daily usage response: %w", err)
	}
	return &out, resp, nil
}
