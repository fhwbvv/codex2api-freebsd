package admin

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	resetRadarSourceName        = "Codex Reset Radar"
	resetRadarBaseURL           = "https://codex-reset-radar.pages.dev/"
	resetRadarCurrentURL        = resetRadarBaseURL + "current.json"
	resetRadarRSSURL            = resetRadarBaseURL + "feed.xml"
	resetRadarCacheTTL          = 60 * time.Second
	resetRadarTimeout           = 2 * time.Second
	resetRadarMaxBytes          = 512 * 1024
	resetRadarHookTimeout       = 30 * time.Minute
	resetRadarCloseSignalMaxAge = 48 * time.Hour
)

var resetRadarHTTPClient = &http.Client{Timeout: resetRadarTimeout}

type resetRadarCacheEntry struct {
	data      resetRadarResponse
	expiresAt time.Time
}

var resetRadarCache struct {
	sync.Mutex
	entry *resetRadarCacheEntry
}

type resetRadarResponse struct {
	SourceName        string                          `json:"source_name"`
	SourceURL         string                          `json:"source_url"`
	RSSURL            string                          `json:"rss_url"`
	CurrentStatusURL  string                          `json:"current_status_url"`
	FetchedAt         string                          `json:"fetched_at"`
	Cached            bool                            `json:"cached"`
	SchemaVersion     string                          `json:"schema_version"`
	Status            string                          `json:"status"`
	WindowOpen        bool                            `json:"window_open"`
	Message           string                          `json:"message"`
	RecommendedAction string                          `json:"recommended_action"`
	CheckedAt         string                          `json:"checked_at"`
	MonitoredAt       string                          `json:"monitored_at"`
	CurrentWindow     resetRadarCurrentWindowResponse `json:"current_window"`
	LastWindow        resetRadarLastWindowResponse    `json:"last_window"`
	Metrics           resetRadarMetricsResponse       `json:"metrics"`
	Prediction        resetRadarPredictionResponse    `json:"prediction"`
	Feed              resetRadarFeedResponse          `json:"feed"`
	Hook              resetRadarHookResponse          `json:"hook"`
}

type resetRadarCurrentWindowResponse struct {
	State    string  `json:"state"`
	Message  string  `json:"message"`
	OpenedAt *string `json:"opened_at"`
	Source   *string `json:"source"`
}

type resetRadarLastWindowResponse struct {
	ID            string                   `json:"id"`
	Title         string                   `json:"title"`
	Status        string                   `json:"status"`
	OpenedAt      string                   `json:"opened_at"`
	ClosedAt      string                   `json:"closed_at"`
	WindowMinutes int                      `json:"window_minutes"`
	WindowHuman   string                   `json:"window_human"`
	Scope         string                   `json:"scope"`
	Summary       string                   `json:"summary"`
	Sources       []resetRadarSourceRecord `json:"sources"`
}

type resetRadarSourceRecord struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type resetRadarMetricsResponse struct {
	Last3MonthsWindowMinutes int    `json:"last_3_months_window_minutes"`
	Last3MonthsWindowHuman   string `json:"last_3_months_window_human"`
}

type resetRadarPredictionResponse struct {
	Level            string                          `json:"level"`
	Probability24H   float64                         `json:"probability_24h"`
	Probability48H   float64                         `json:"probability_48h"`
	ExpectedWindow   string                          `json:"expected_window"`
	ReasoningSummary string                          `json:"reasoning_summary"`
	ShouldNotify     bool                            `json:"should_notify"`
	UpdatedAt        string                          `json:"updated_at"`
	SignalSummary24H resetRadarSignalSummaryResponse `json:"signal_summary_24h"`
	Source           string                          `json:"source"`
}

type resetRadarSignalSummaryResponse struct {
	Total  int                            `json:"total"`
	Counts resetRadarSignalCountsResponse `json:"counts"`
	Top    []resetRadarTopSignalResponse  `json:"top_signals"`
}

type resetRadarSignalCountsResponse struct {
	OpenAIStatus int `json:"openai_status"`
	OfficialX    int `json:"official_x"`
	CommunityX   int `json:"community_x"`
	XReply       int `json:"x_reply"`
	MarketX      int `json:"market_x"`
}

type resetRadarTopSignalResponse struct {
	Source string  `json:"source"`
	Score  float64 `json:"score"`
	Text   string  `json:"text"`
	URL    string  `json:"url"`
}

type resetRadarFeedResponse struct {
	Title         string               `json:"title"`
	Description   string               `json:"description"`
	LastBuildDate string               `json:"last_build_date"`
	TTL           int                  `json:"ttl"`
	Error         string               `json:"error,omitempty"`
	Items         []resetRadarFeedItem `json:"items"`
}

type resetRadarFeedItem struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	GUID        string `json:"guid"`
	PubDate     string `json:"pub_date"`
	PublishedAt string `json:"published_at"`
	Summary     string `json:"summary"`
	Event       string `json:"event"`
}

type resetRadarHookResponse struct {
	SignalDetected        bool                  `json:"signal_detected"`
	SignalID              string                `json:"signal_id,omitempty"`
	SignalType            string                `json:"signal_type,omitempty"`
	Triggered             bool                  `json:"triggered"`
	Running               bool                  `json:"running"`
	LastTriggeredSignalID string                `json:"last_triggered_signal_id,omitempty"`
	LastTriggeredAt       string                `json:"last_triggered_at,omitempty"`
	LastCompletedAt       string                `json:"last_completed_at,omitempty"`
	LastResult            *resetRadarHookResult `json:"last_result,omitempty"`
	Message               string                `json:"message"`
}

type resetRadarHookResult struct {
	Total       int64  `json:"total"`
	Success     int64  `json:"success"`
	Failed      int64  `json:"failed"`
	Banned      int64  `json:"banned"`
	RateLimited int64  `json:"rate_limited"`
	Error       string `json:"error,omitempty"`
}

type resetRadarHookState struct {
	running               bool
	lastTriggeredSignalID string
	lastTriggeredAt       time.Time
	lastCompletedAt       time.Time
	lastResult            *resetRadarHookResult
}

type resetRadarHookSignal struct {
	ID   string
	Type string
}

type resetRadarUpstreamPayload struct {
	SchemaVersion     string                          `json:"schema_version"`
	Status            string                          `json:"status"`
	WindowOpen        bool                            `json:"window_open"`
	Message           string                          `json:"message"`
	RecommendedAction string                          `json:"recommended_action"`
	CheckedAt         string                          `json:"checked_at"`
	MonitoredAt       string                          `json:"monitored_at"`
	CurrentWindow     resetRadarCurrentWindowResponse `json:"current_window"`
	LastWindow        resetRadarLastWindowResponse    `json:"last_window"`
	Metrics           resetRadarMetricsResponse       `json:"metrics"`
	Prediction        resetRadarPredictionResponse    `json:"prediction"`
}

type resetRadarRSSPayload struct {
	Channel resetRadarRSSChannel `xml:"channel"`
}

type resetRadarRSSChannel struct {
	Title         string              `xml:"title"`
	Description   string              `xml:"description"`
	LastBuildDate string              `xml:"lastBuildDate"`
	TTL           int                 `xml:"ttl"`
	Items         []resetRadarRSSItem `xml:"item"`
}

type resetRadarRSSItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

// GetResetRadar returns a small attributed status summary from Codex Reset Radar.
// It intentionally does not mirror the external page content or styling.
func (h *Handler) GetResetRadar(c *gin.Context) {
	data, cached, err := getResetRadarSnapshot(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusBadGateway, err.Error())
		return
	}
	data.Cached = cached
	data.Hook = h.maybeTriggerResetRadarHook(data)
	c.JSON(http.StatusOK, data)
}

func (h *Handler) maybeTriggerResetRadarHook(data resetRadarResponse) resetRadarHookResponse {
	signal := resetRadarHookSignalFor(data)
	signalDetected := signal.ID != ""

	if h == nil {
		return resetRadarHookResponse{
			SignalDetected: signalDetected,
			SignalID:       signal.ID,
			SignalType:     signal.Type,
			Message:        "handler_unavailable",
		}
	}

	h.resetRadarHookMu.Lock()
	defer h.resetRadarHookMu.Unlock()

	resp := h.resetRadarHookSnapshotLocked(signalDetected, signal)
	if !signalDetected {
		resp.Message = "waiting_for_reset_signal"
		return resp
	}
	if h.store == nil {
		resp.Message = "account_store_unavailable"
		return resp
	}
	if h.resetRadarHookState.running {
		resp.Message = "hook_running"
		return resp
	}
	if h.resetRadarHookState.lastTriggeredSignalID == signal.ID {
		resp.Message = "signal_already_handled"
		return resp
	}

	h.resetRadarHookState.running = true
	h.resetRadarHookState.lastTriggeredSignalID = signal.ID
	h.resetRadarHookState.lastTriggeredAt = time.Now()
	h.resetRadarHookState.lastResult = nil
	resp = h.resetRadarHookSnapshotLocked(signalDetected, signal)
	resp.Triggered = true
	resp.Message = "hook_triggered"

	runner := h.resetRadarHookRunner
	if runner == nil {
		runner = h.runResetRadarSignalHook
	}
	go h.finishResetRadarSignalHook(signal.ID, runner)

	return resp
}

func (h *Handler) resetRadarHookSnapshotLocked(signalDetected bool, signal resetRadarHookSignal) resetRadarHookResponse {
	resp := resetRadarHookResponse{
		SignalDetected:        signalDetected,
		SignalID:              signal.ID,
		SignalType:            signal.Type,
		Running:               h.resetRadarHookState.running,
		LastTriggeredSignalID: h.resetRadarHookState.lastTriggeredSignalID,
		LastResult:            h.resetRadarHookState.lastResult,
	}
	if !h.resetRadarHookState.lastTriggeredAt.IsZero() {
		resp.LastTriggeredAt = h.resetRadarHookState.lastTriggeredAt.Format(time.RFC3339)
	}
	if !h.resetRadarHookState.lastCompletedAt.IsZero() {
		resp.LastCompletedAt = h.resetRadarHookState.lastCompletedAt.Format(time.RFC3339)
	}
	return resp
}

func (h *Handler) finishResetRadarSignalHook(signalID string, runner func(context.Context, string) resetRadarHookResult) {
	result := resetRadarHookResult{}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				result.Error = fmt.Sprintf("panic: %v", recovered)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), resetRadarHookTimeout)
		defer cancel()
		result = runner(ctx, signalID)
	}()

	h.resetRadarHookMu.Lock()
	h.resetRadarHookState.running = false
	h.resetRadarHookState.lastCompletedAt = time.Now()
	h.resetRadarHookState.lastResult = &result
	h.resetRadarHookMu.Unlock()
}

func (h *Handler) runResetRadarSignalHook(ctx context.Context, signalID string) resetRadarHookResult {
	result := resetRadarHookResult{}
	if h == nil || h.store == nil {
		result.Error = "account_store_unavailable"
		return result
	}

	accounts := h.store.Accounts()
	for _, acc := range accounts {
		if acc == nil {
			continue
		}
		h.store.ClearCooldown(acc)
		acc.ClearUsageCache()
	}

	log.Printf("收到 Codex Reset Radar 重置信号 %s，开始批量测试 %d 个账号以刷新状态和用量窗口", signalID, len(accounts))
	counts := h.runBatchTest(ctx, accounts, 0, h.runSingleBatchTest, nil)
	result.Total = int64(counts.Total)
	result.Success = counts.Success
	result.Failed = counts.Failed
	result.Banned = counts.Banned
	result.RateLimited = counts.RateLimited
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
	}
	log.Printf("Codex Reset Radar 重置信号钩子完成: signal=%s total=%d success=%d failed=%d banned=%d rate_limited=%d error=%s",
		signalID, result.Total, result.Success, result.Failed, result.Banned, result.RateLimited, result.Error)
	return result
}

func resetRadarHookSignalFor(data resetRadarResponse) resetRadarHookSignal {
	if id := resetRadarCloseSignalID(data); id != "" {
		return resetRadarHookSignal{ID: id, Type: "close"}
	}
	return resetRadarHookSignal{}
}

func resetRadarCloseSignalID(data resetRadarResponse) string {
	closedAt := strings.TrimSpace(data.LastWindow.ClosedAt)
	if closedAt == "" || !resetRadarCloseSignalIsFresh(data) {
		return ""
	}
	parts := []string{"close", closedAt}
	for _, value := range []string{
		strings.TrimSpace(data.LastWindow.ID),
		strings.TrimSpace(data.LastWindow.Status),
		resetRadarLastWindowSourceURL(data.LastWindow, "window_closed"),
	} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "|")
}

func resetRadarCloseSignalIsFresh(data resetRadarResponse) bool {
	closedAt, ok := parseResetRadarTimestamp(data.LastWindow.ClosedAt)
	if !ok {
		return false
	}
	reference := time.Now()
	for _, value := range []string{data.CheckedAt, data.MonitoredAt, data.FetchedAt} {
		if parsed, parsedOK := parseResetRadarTimestamp(value); parsedOK {
			reference = parsed
			break
		}
	}
	if closedAt.After(reference.Add(5 * time.Minute)) {
		return false
	}
	return !closedAt.Before(reference.Add(-resetRadarCloseSignalMaxAge))
}

func resetRadarLastWindowSourceURL(window resetRadarLastWindowResponse, sourceType string) string {
	sourceType = strings.TrimSpace(sourceType)
	for _, source := range window.Sources {
		if sourceType == "" || strings.EqualFold(strings.TrimSpace(source.Type), sourceType) {
			return strings.TrimSpace(source.URL)
		}
	}
	return ""
}

func parseResetRadarTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func getResetRadarSnapshot(ctx context.Context) (resetRadarResponse, bool, error) {
	now := time.Now()
	resetRadarCache.Lock()
	if resetRadarCache.entry != nil && now.Before(resetRadarCache.entry.expiresAt) {
		data := resetRadarCache.entry.data
		resetRadarCache.Unlock()
		return data, true, nil
	}
	resetRadarCache.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, resetRadarTimeout)
	defer cancel()
	data, err := fetchResetRadarSnapshot(fetchCtx, resetRadarCurrentURL, resetRadarHTTPClient)
	if err == nil {
		resetRadarCache.Lock()
		resetRadarCache.entry = &resetRadarCacheEntry{
			data:      data,
			expiresAt: time.Now().Add(resetRadarCacheTTL),
		}
		resetRadarCache.Unlock()
		return data, false, nil
	}

	resetRadarCache.Lock()
	defer resetRadarCache.Unlock()
	if resetRadarCache.entry != nil {
		data := resetRadarCache.entry.data
		return data, true, nil
	}
	return resetRadarResponse{}, false, fmt.Errorf("无法获取 Codex Reset Radar 状态: %w", err)
}

func fetchResetRadarSnapshot(ctx context.Context, url string, client *http.Client) (resetRadarResponse, error) {
	if client == nil {
		client = resetRadarHTTPClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return resetRadarResponse{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return resetRadarResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resetRadarResponse{}, fmt.Errorf("上游返回 HTTP %d", resp.StatusCode)
	}

	var payload resetRadarUpstreamPayload
	limited := io.LimitReader(resp.Body, resetRadarMaxBytes)
	if err := json.NewDecoder(limited).Decode(&payload); err != nil {
		return resetRadarResponse{}, err
	}

	data := resetRadarResponse{
		SourceName:        resetRadarSourceName,
		SourceURL:         resetRadarBaseURL,
		RSSURL:            resetRadarRSSURL,
		CurrentStatusURL:  resetRadarCurrentURL,
		FetchedAt:         time.Now().Format(time.RFC3339),
		SchemaVersion:     payload.SchemaVersion,
		Status:            payload.Status,
		WindowOpen:        payload.WindowOpen,
		Message:           payload.Message,
		RecommendedAction: payload.RecommendedAction,
		CheckedAt:         payload.CheckedAt,
		MonitoredAt:       payload.MonitoredAt,
		CurrentWindow:     payload.CurrentWindow,
		LastWindow:        payload.LastWindow,
		Metrics:           payload.Metrics,
		Prediction:        payload.Prediction,
	}

	feed, feedErr := fetchResetRadarFeed(ctx, resetRadarRSSURL, client)
	if feedErr != nil {
		feed.Error = feedErr.Error()
	}
	data.Feed = feed
	return data, nil
}

func fetchResetRadarFeed(ctx context.Context, url string, client *http.Client) (resetRadarFeedResponse, error) {
	if client == nil {
		client = resetRadarHTTPClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return resetRadarFeedResponse{}, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml")

	resp, err := client.Do(req)
	if err != nil {
		return resetRadarFeedResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resetRadarFeedResponse{}, fmt.Errorf("RSS 上游返回 HTTP %d", resp.StatusCode)
	}

	var payload resetRadarRSSPayload
	limited := io.LimitReader(resp.Body, resetRadarMaxBytes)
	if err := xml.NewDecoder(limited).Decode(&payload); err != nil {
		return resetRadarFeedResponse{}, err
	}

	items := make([]resetRadarFeedItem, 0, minInt(len(payload.Channel.Items), 8))
	for _, item := range payload.Channel.Items {
		if len(items) >= 8 {
			break
		}
		items = append(items, resetRadarFeedItem{
			Title:       strings.TrimSpace(item.Title),
			Link:        strings.TrimSpace(item.Link),
			GUID:        strings.TrimSpace(item.GUID),
			PubDate:     strings.TrimSpace(item.PubDate),
			PublishedAt: parseRSSDate(item.PubDate),
			Summary:     truncateRunes(compactWhitespace(item.Description), 180),
			Event:       classifyResetRadarEvent(item.Title),
		})
	}

	return resetRadarFeedResponse{
		Title:         strings.TrimSpace(payload.Channel.Title),
		Description:   strings.TrimSpace(payload.Channel.Description),
		LastBuildDate: strings.TrimSpace(payload.Channel.LastBuildDate),
		TTL:           payload.Channel.TTL,
		Items:         items,
	}, nil
}

func parseRSSDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format(time.RFC3339)
		}
	}
	return ""
}

func classifyResetRadarEvent(title string) string {
	title = strings.TrimSpace(title)
	switch {
	case strings.Contains(title, "开启"):
		return "open"
	case strings.Contains(title, "关闭"):
		return "close"
	default:
		return "info"
	}
}

func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
