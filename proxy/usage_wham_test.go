package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
)

func TestQueryWhamUsage_ParsesPlusAccountResponse(t *testing.T) {
	body := `{
		"user_id": "user-abc",
		"account_id": "user-abc",
		"email": "rundown_consist_3o@icloud.com",
		"plan_type": "plus",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {
				"used_percent": 83,
				"limit_window_seconds": 18000,
				"reset_after_seconds": 10778,
				"reset_at": 1779708117
			},
			"secondary_window": {
				"used_percent": 30,
				"limit_window_seconds": 604800,
				"reset_after_seconds": 474764,
				"reset_at": 1780172103
			}
		},
		"credits": {
			"has_credits": false,
			"unlimited": false,
			"overage_limit_reached": false,
			"balance": "0",
			"approx_local_messages": [0, 0],
			"approx_cloud_messages": [0, 0]
		},
		"spend_control": {"reached": false, "individual_limit": null}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("Authorization header missing or malformed: %q", got)
		}
		if r.Header.Get("chatgpt-account-id") != "acc-1" {
			t.Errorf("chatgpt-account-id = %q, want acc-1", r.Header.Get("chatgpt-account-id"))
		}
		if r.Header.Get("Originator") != Originator {
			t.Errorf("Originator = %q, want %q", r.Header.Get("Originator"), Originator)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "codex_cli_rs/") {
			t.Errorf("User-Agent = %q, want codex_cli_rs prefix", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	// 重写 URL 到测试服务器：通过临时变量替换
	oldURL := whamURLForTest
	whamURLForTest = server.URL
	defer func() { whamURLForTest = oldURL }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123", AccountID: "acc-1"}
	usage, _, err := queryWhamUsageWithURL(context.Background(), account, "", whamURLForTest)
	if err != nil {
		t.Fatalf("QueryWhamUsage error: %v", err)
	}
	if usage.PlanType != "plus" {
		t.Errorf("PlanType = %q, want plus", usage.PlanType)
	}
	if usage.RateLimit.PrimaryWindow == nil || usage.RateLimit.PrimaryWindow.UsedPercent != 83 {
		t.Errorf("primary used_percent = %+v, want 83", usage.RateLimit.PrimaryWindow)
	}
	if usage.RateLimit.SecondaryWindow == nil || usage.RateLimit.SecondaryWindow.UsedPercent != 30 {
		t.Errorf("secondary used_percent = %+v, want 30", usage.RateLimit.SecondaryWindow)
	}
}

func TestApplyWhamUsage_PersistsPlanAnd5h7d(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	id, err := db.InsertAccountWithCredentials(ctx, "wham-test", map[string]interface{}{"plan_type": "free"}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithCredentials: %v", err)
	}

	store := auth.NewStore(db, nil, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1, TestModel: "gpt-5.4"})
	account := &auth.Account{DBID: id, AccessToken: "at", PlanType: "free", AccountID: "acc"}

	now := time.Now()
	reset5h := now.Add(3 * time.Hour).Unix()
	reset7d := now.Add(5 * 24 * time.Hour).Unix()
	usage := &WhamUsage{PlanType: "plus"}
	usage.RateLimit.PrimaryWindow = &WhamUsageWindow{UsedPercent: 83, LimitWindowSeconds: 18000, ResetAt: reset5h}
	usage.RateLimit.SecondaryWindow = &WhamUsageWindow{UsedPercent: 30, LimitWindowSeconds: 604800, ResetAt: reset7d}

	result := ApplyWhamUsage(store, account, usage)

	if got := account.GetPlanType(); got != "plus" {
		t.Errorf("plan_type = %q, want plus (synced from wham)", got)
	}
	if !result.HasUsage5h || result.UsagePct5h != 83 {
		t.Errorf("5h result = %+v, want HasUsage5h && UsagePct5h=83", result)
	}
	if !result.HasUsage7d || result.UsagePct7d != 30 {
		t.Errorf("7d result = %+v, want HasUsage7d && UsagePct7d=30", result)
	}
	if result.Premium5hRateLimited {
		t.Error("expected NOT premium 5h rate limited (used_percent=83 < 100)")
	}

	row, err := db.GetAccountByID(ctx, id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if got := row.GetCredential("plan_type"); got != "plus" {
		t.Errorf("persisted plan_type = %q, want plus", got)
	}
}

// 复现 issue #168：free 账号的 wham 响应里 primary_window 实际承载的是 7d 数据
// （limit_window_seconds=604800），secondary_window=null。代码必须按
// limit_window_seconds 而不是字段位置来分类，否则 7d 数据会被错误写入 5h 槽位。
func TestApplyWhamUsage_FreeAccountPrimaryIs7d(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	id, err := db.InsertAccountWithCredentials(ctx, "wham-free", map[string]interface{}{"plan_type": "free"}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithCredentials: %v", err)
	}

	store := auth.NewStore(db, nil, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1, TestModel: "gpt-5.4"})
	account := &auth.Account{DBID: id, AccessToken: "at", PlanType: "free", AccountID: "acc"}

	reset7d := time.Now().Add(7 * 24 * time.Hour).Unix()
	usage := &WhamUsage{PlanType: "free"}
	usage.RateLimit.PrimaryWindow = &WhamUsageWindow{UsedPercent: 3, LimitWindowSeconds: 604800, ResetAfterSeconds: 604800, ResetAt: reset7d}
	usage.RateLimit.SecondaryWindow = nil

	result := ApplyWhamUsage(store, account, usage)

	if result.HasUsage5h {
		t.Errorf("expected HasUsage5h=false for free account (only 7d window), got result=%+v", result)
	}
	if result.Used5hHeaders {
		t.Errorf("expected Used5hHeaders=false (no 5h window in response), got result=%+v", result)
	}
	if !result.HasUsage7d || result.UsagePct7d != 3 {
		t.Errorf("7d result = %+v, want HasUsage7d && UsagePct7d=3", result)
	}
	if result.Persisted5hOnly {
		t.Error("expected Persisted5hOnly=false; should persist via 7d snapshot path")
	}

	if pct, ok := account.GetUsagePercent7d(); !ok || pct != 3 {
		t.Errorf("account 7d in-memory snapshot = (%v, %v), want (3, true)", pct, ok)
	}
	if _, ok := account.GetUsagePercent5h(); ok {
		t.Error("account 5h snapshot should remain unset for free account")
	}

	row, err := db.GetAccountByID(ctx, id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if got := row.GetCredential("codex_7d_used_percent"); got != "3" {
		t.Errorf("persisted codex_7d_used_percent = %q, want %q", got, "3")
	}
}

// 防御性测试：即使后端把 5h/7d 字段顺序对调，分类也必须按 limit_window_seconds 走。
func TestApplyWhamUsage_ClassifiesByWindowSeconds(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1, TestModel: "gpt-5.4"})
	account := &auth.Account{DBID: 1, AccessToken: "at", PlanType: "plus"}

	now := time.Now()
	usage := &WhamUsage{PlanType: "plus"}
	// 故意颠倒：把 7d (604800) 放 primary、5h (18000) 放 secondary
	usage.RateLimit.PrimaryWindow = &WhamUsageWindow{UsedPercent: 30, LimitWindowSeconds: 604800, ResetAt: now.Add(5 * 24 * time.Hour).Unix()}
	usage.RateLimit.SecondaryWindow = &WhamUsageWindow{UsedPercent: 83, LimitWindowSeconds: 18000, ResetAt: now.Add(3 * time.Hour).Unix()}

	result := ApplyWhamUsage(store, account, usage)

	if !result.HasUsage5h || result.UsagePct5h != 83 {
		t.Errorf("5h result = %+v, want UsagePct5h=83 (classified by 18000s window)", result)
	}
	if !result.HasUsage7d || result.UsagePct7d != 30 {
		t.Errorf("7d result = %+v, want UsagePct7d=30 (classified by 604800s window)", result)
	}
}

// 当 limit_window_seconds 缺失或为未知值时，按字段位置兜底分类
// （与 CPA-Manager pickClassifiedWindows 的 allowOrderFallback 行为一致）。
func TestPickClassifiedWhamWindows_FallsBackToPositionForUnknownSeconds(t *testing.T) {
	primary := &WhamUsageWindow{UsedPercent: 50, LimitWindowSeconds: 0} // 未知/缺失
	secondary := &WhamUsageWindow{UsedPercent: 20, LimitWindowSeconds: 0}

	w5h, w7d := pickClassifiedWhamWindows(primary, secondary, "plus", time.Now())
	if w5h != primary {
		t.Errorf("expected primary→5h via position fallback, got %v", w5h)
	}
	if w7d != secondary {
		t.Errorf("expected secondary→7d via position fallback, got %v", w7d)
	}
}

func TestPickClassifiedWhamWindows_FreeUnknownPrimaryFallsBackTo7d(t *testing.T) {
	primary := &WhamUsageWindow{UsedPercent: 100, LimitWindowSeconds: 0}

	w5h, w7d := pickClassifiedWhamWindows(primary, nil, "free", time.Now())
	if w5h != nil {
		t.Fatalf("expected no 5h window for free unknown primary, got %v", w5h)
	}
	if w7d != primary {
		t.Fatalf("expected primary→7d for free unknown primary, got %v", w7d)
	}
}

func TestPickClassifiedWhamWindows_LongResetPrimaryFallsBackTo7d(t *testing.T) {
	primary := &WhamUsageWindow{
		UsedPercent:        100,
		LimitWindowSeconds: 0,
		ResetAfterSeconds:  6 * 60 * 60,
	}

	w5h, w7d := pickClassifiedWhamWindows(primary, nil, "", time.Now())
	if w5h != nil {
		t.Fatalf("expected no 5h window for long reset primary, got %v", w5h)
	}
	if w7d != primary {
		t.Fatalf("expected primary→7d for long reset primary, got %v", w7d)
	}
}

func TestApplyWhamUsage_MarksPremium5hLimitedAt100Percent(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1, TestModel: "gpt-5.4"})
	account := &auth.Account{DBID: 1, AccessToken: "at", PlanType: "plus"}

	now := time.Now()
	usage := &WhamUsage{PlanType: "plus"}
	usage.RateLimit.PrimaryWindow = &WhamUsageWindow{UsedPercent: 100, LimitWindowSeconds: 18000, ResetAt: now.Add(2 * time.Hour).Unix()}

	result := ApplyWhamUsage(store, account, usage)
	if !result.Premium5hRateLimited {
		t.Errorf("expected Premium5hRateLimited=true for plus plan at 100%%, result=%+v", result)
	}
	if !account.IsPremium5hRateLimited() {
		t.Error("account should be in premium 5h rate-limited state after ApplyWhamUsage")
	}
}

func TestWhamUsageJSON_RoundTrip(t *testing.T) {
	in := WhamUsage{PlanType: "plus"}
	in.RateLimit.Allowed = true
	in.RateLimit.PrimaryWindow = &WhamUsageWindow{UsedPercent: 50}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out WhamUsage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.RateLimit.PrimaryWindow == nil || out.RateLimit.PrimaryWindow.UsedPercent != 50 {
		t.Errorf("roundtrip lost primary window")
	}
}
