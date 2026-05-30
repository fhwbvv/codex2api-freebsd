package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
)

type resetRadarRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn resetRadarRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestFetchResetRadarSnapshotMapsAttributedSummary(t *testing.T) {
	client := &http.Client{Transport: resetRadarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/feed.xml") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`<rss version="2.0"><channel><title>Codex 重置雷达</title><ttl>10</ttl></channel></rss>`)),
			}, nil
		}
		if got := req.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q, want application/json", got)
		}
		body := `{
			"schema_version":"1.0",
			"status":"open",
			"window_open":true,
			"message":"窗口开启",
			"recommended_action":"use_remaining_quota",
			"checked_at":"2026-05-27T01:54:43+08:00",
			"monitored_at":"2026-05-27T01:54:43+08:00",
			"current_window":{"state":"open","message":"当前窗口开启","opened_at":"2026-05-27T01:00:00+08:00","source":"https://x.com/example/status/1"},
			"last_window":{"id":"win-1","title":"Codex 用量重置","status":"closed","opened_at":"2026-05-23T08:21:33+08:00","closed_at":"2026-05-24T04:14:35+08:00","window_minutes":1193,"window_human":"19小时53分","scope":"Codex 用户","summary":"官方发布 Codex 用量重置信号。","sources":[{"type":"window_opened","url":"https://x.com/example/status/1"}]},
			"metrics":{"last_3_months_window_minutes":5161,"last_3_months_window_human":"86小时1分"},
			"prediction":{"level":"high","probability_24h":0.8,"probability_48h":0.9,"expected_window":"未来 24-48 小时","reasoning_summary":"有强信号。","should_notify":true,"updated_at":"2026-05-27T01:54:43+08:00","signal_summary_24h":{"total":2,"counts":{"official_x":1,"x_reply":1},"top_signals":[{"source":"official_x","score":99,"text":"reset soon","url":"https://x.com/example/status/1"}]},"source":"prediction.json"}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	got, err := fetchResetRadarSnapshot(context.Background(), "https://example.test/current.json", client)
	if err != nil {
		t.Fatalf("fetchResetRadarSnapshot returned error: %v", err)
	}

	if got.SourceURL != resetRadarBaseURL || got.RSSURL != resetRadarRSSURL || got.CurrentStatusURL != resetRadarCurrentURL {
		t.Fatalf("source links not populated correctly: %+v", got)
	}
	if !got.WindowOpen || got.Status != "open" || got.RecommendedAction != "use_remaining_quota" {
		t.Fatalf("status fields = %+v", got)
	}
	if got.LastWindow.ID != "win-1" || got.LastWindow.Sources[0].URL == "" {
		t.Fatalf("last window fields = %+v", got.LastWindow)
	}
	if got.Prediction.Level != "high" || got.Prediction.SignalSummary24H.Total != 2 {
		t.Fatalf("prediction fields = %+v", got.Prediction)
	}
	if got.FetchedAt == "" {
		t.Fatal("FetchedAt was empty")
	}
}

func TestFetchResetRadarFeedMapsRecentItems(t *testing.T) {
	client := &http.Client{Transport: resetRadarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Codex 重置雷达</title>
    <description>只发布 Codex 速蹬窗口开启和关闭提醒。</description>
    <ttl>10</ttl>
    <lastBuildDate>Sat, 23 May 2026 21:46:57 GMT</lastBuildDate>
    <item>
      <title>速蹬窗口开启：Codex 用量重置</title>
      <link>https://codex-reset-radar.pages.dev/#open</link>
      <guid>open-1</guid>
      <pubDate>Sat, 23 May 2026 00:21:33 GMT</pubDate>
      <description><![CDATA[
发现有效重置预告，速蹬窗口开启。

窗口开启：5月23日 08:21 UTC+8
      ]]></description>
    </item>
    <item>
      <title>速蹬窗口关闭：Codex 用量重置</title>
      <link>https://codex-reset-radar.pages.dev/#close</link>
      <guid>close-1</guid>
      <pubDate>Sat, 23 May 2026 20:14:35 GMT</pubDate>
      <description>速蹬窗口已关闭。</description>
    </item>
  </channel>
</rss>`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	got, err := fetchResetRadarFeed(context.Background(), "https://example.test/feed.xml", client)
	if err != nil {
		t.Fatalf("fetchResetRadarFeed returned error: %v", err)
	}
	if got.Title != "Codex 重置雷达" || got.TTL != 10 {
		t.Fatalf("feed metadata = %+v", got)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(got.Items))
	}
	if got.Items[0].Event != "open" || got.Items[1].Event != "close" {
		t.Fatalf("events = %q/%q, want open/close", got.Items[0].Event, got.Items[1].Event)
	}
	if got.Items[0].PublishedAt != "2026-05-23T00:21:33Z" {
		t.Fatalf("PublishedAt = %q", got.Items[0].PublishedAt)
	}
	if strings.Contains(got.Items[0].Summary, "\n") {
		t.Fatalf("summary was not compacted: %q", got.Items[0].Summary)
	}
}

func TestGetResetRadarSnapshotFallsBackToCachedData(t *testing.T) {
	resetRadarCache.Lock()
	resetRadarCache.entry = &resetRadarCacheEntry{
		data: resetRadarResponse{
			SourceName: resetRadarSourceName,
			Message:    "cached status",
		},
		expiresAt: time.Now().Add(-time.Second),
	}
	resetRadarCache.Unlock()

	originalClient := resetRadarHTTPClient
	resetRadarHTTPClient = &http.Client{Transport: resetRadarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}
	defer func() {
		resetRadarHTTPClient = originalClient
		resetRadarCache.Lock()
		resetRadarCache.entry = nil
		resetRadarCache.Unlock()
	}()

	got, cached, err := getResetRadarSnapshot(context.Background())
	if err != nil {
		t.Fatalf("getResetRadarSnapshot returned error: %v", err)
	}
	if !cached {
		t.Fatal("cached = false, want true")
	}
	if got.Message != "cached status" {
		t.Fatalf("Message = %q, want cached status", got.Message)
	}
}

func TestResetRadarHookSignalIDRequiresFreshCloseSignal(t *testing.T) {
	if got := resetRadarHookSignalFor(resetRadarResponse{Status: "closed"}); got.ID != "" {
		t.Fatalf("signal id = %q, want empty", got)
	}

	openedAt := "2026-05-27T01:00:00+08:00"
	source := "https://x.com/example/status/1"
	if openSignal := resetRadarHookSignalFor(resetRadarResponse{
		WindowOpen: true,
		Status:     "open",
		CurrentWindow: resetRadarCurrentWindowResponse{
			OpenedAt: &openedAt,
			Source:   &source,
		},
		LastWindow: resetRadarLastWindowResponse{ID: "win-1"},
	}); openSignal.ID != "" {
		t.Fatalf("open signal = %+v, want empty because hooks only run on close", openSignal)
	}

	closeSignal := resetRadarHookSignalFor(resetRadarResponse{
		CheckedAt: "2026-05-24T04:15:35+08:00",
		LastWindow: resetRadarLastWindowResponse{
			ID:       "win-1",
			Status:   "closed",
			ClosedAt: "2026-05-24T04:14:35+08:00",
			Sources: []resetRadarSourceRecord{
				{Type: "window_closed", URL: "https://x.com/example/status/2"},
			},
		},
	})
	if closeSignal.Type != "close" || !strings.Contains(closeSignal.ID, "2026-05-24T04:14:35+08:00") || !strings.Contains(closeSignal.ID, "win-1") {
		t.Fatalf("close signal missing stable fields: %+v", closeSignal)
	}

	staleClose := resetRadarHookSignalFor(resetRadarResponse{
		CheckedAt: "2026-05-27T04:51:19+08:00",
		LastWindow: resetRadarLastWindowResponse{
			ID:       "win-1",
			Status:   "closed",
			ClosedAt: "2026-05-24T04:14:35+08:00",
		},
	})
	if staleClose.ID != "" {
		t.Fatalf("stale close signal = %+v, want empty", staleClose)
	}
}

func TestResetRadarHookTriggersOncePerSignal(t *testing.T) {
	handler := &Handler{store: auth.NewStore(nil, nil, nil)}

	started := make(chan string, 1)
	release := make(chan struct{})
	handler.resetRadarHookRunner = func(_ context.Context, signalID string) resetRadarHookResult {
		started <- signalID
		<-release
		return resetRadarHookResult{Total: 2, Success: 1, Failed: 1}
	}

	data := resetRadarResponse{
		CheckedAt: "2026-05-24T04:15:35+08:00",
		LastWindow: resetRadarLastWindowResponse{
			ID:       "win-1",
			Status:   "closed",
			ClosedAt: "2026-05-24T04:14:35+08:00",
		},
	}

	first := handler.maybeTriggerResetRadarHook(data)
	if !first.SignalDetected || !first.Triggered || !first.Running || first.Message != "hook_triggered" {
		t.Fatalf("first hook response = %+v", first)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("hook runner did not start")
	}

	second := handler.maybeTriggerResetRadarHook(data)
	if second.Triggered || !second.Running || second.Message != "hook_running" {
		t.Fatalf("second hook response while running = %+v", second)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		handler.resetRadarHookMu.Lock()
		running := handler.resetRadarHookState.running
		handler.resetRadarHookMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hook runner did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}

	third := handler.maybeTriggerResetRadarHook(data)
	if third.Triggered || third.Running || third.Message != "signal_already_handled" {
		t.Fatalf("third hook response after completion = %+v", third)
	}
	if third.LastResult == nil || third.LastResult.Total != 2 || third.LastResult.Success != 1 {
		t.Fatalf("last result = %+v", third.LastResult)
	}
}

func TestResetRadarHookIgnoresOpenAndTriggersClose(t *testing.T) {
	handler := &Handler{store: auth.NewStore(nil, nil, nil)}
	calls := make(chan string, 2)
	handler.resetRadarHookRunner = func(_ context.Context, signalID string) resetRadarHookResult {
		calls <- signalID
		return resetRadarHookResult{Total: 1, Success: 1}
	}

	openedAt := "2026-05-24T03:00:00+08:00"
	openData := resetRadarResponse{
		WindowOpen: true,
		CurrentWindow: resetRadarCurrentWindowResponse{
			OpenedAt: &openedAt,
		},
	}
	first := handler.maybeTriggerResetRadarHook(openData)
	if first.Triggered || first.SignalDetected || first.Message != "waiting_for_reset_signal" {
		t.Fatalf("open hook response = %+v, want waiting without trigger", first)
	}

	closeData := resetRadarResponse{
		CheckedAt: "2026-05-24T04:15:35+08:00",
		LastWindow: resetRadarLastWindowResponse{
			ID:       "win-1",
			Status:   "closed",
			ClosedAt: "2026-05-24T04:14:35+08:00",
		},
	}
	second := handler.maybeTriggerResetRadarHook(closeData)
	if !second.Triggered || second.SignalType != "close" {
		t.Fatalf("close hook response = %+v", second)
	}
	waitForResetRadarHookIdle(t, handler)

	if len(calls) != 1 {
		t.Fatalf("hook call count = %d, want 1", len(calls))
	}
}

func waitForResetRadarHookIdle(t *testing.T, handler *Handler) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		handler.resetRadarHookMu.Lock()
		running := handler.resetRadarHookState.running
		handler.resetRadarHookMu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("hook runner did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
