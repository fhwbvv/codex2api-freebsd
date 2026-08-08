package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex2api/auth"
)

func TestSendCodexInvite_Success(t *testing.T) {
	var gotAuth, gotAccountID, gotOriginator string
	var body struct {
		ProgramID  string   `json:"program_id"`
		Entrypoint string   `json:"entrypoint"`
		Emails     []string `json:"emails"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("Chatgpt-Account-Id")
		gotOriginator = r.Header.Get("Originator")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("x-oai-request-id", "req-123")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"invites":[{"referral_id":"r1","email":"a@example.com","invite_url":"https://x/y"}]}`))
	}))
	defer server.Close()

	old := codexInviteURLForTest
	codexInviteURLForTest = server.URL
	defer func() { codexInviteURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123", AccountID: "acc-1"}
	res, err := SendCodexInvite(context.Background(), account, "", "", "", []string{"a@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if !res.OK || res.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, want OK 200", res)
	}
	if res.RequestID != "req-123" {
		t.Errorf("request_id = %q, want req-123", res.RequestID)
	}
	if res.ProgramID != DefaultProgramID || res.Entrypoint != DefaultEntrypoint {
		t.Errorf("program = (%q,%q), want (%q,%q)", res.ProgramID, res.Entrypoint, DefaultProgramID, DefaultEntrypoint)
	}
	if len(res.Invites) != 1 || res.Invites[0].InviteURL != "https://x/y" {
		t.Errorf("invites = %+v, want 1 parsed item", res.Invites)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccountID != "acc-1" {
		t.Errorf("chatgpt-account-id = %q, want acc-1", gotAccountID)
	}
	if gotOriginator != inviteOriginator {
		t.Errorf("originator = %q, want %q", gotOriginator, inviteOriginator)
	}
	// 上游要求 program_id + entrypoint + emails 三个必填字段，缺一个就是 422。
	if body.ProgramID != DefaultProgramID || body.Entrypoint != DefaultEntrypoint || len(body.Emails) != 1 {
		t.Errorf("upstream body = %+v", body)
	}
}

// 上游发送响应若改用 items[] 而非 invites[]（跟踪端点就是这种形态），明细仍要能解析出来。
func TestSendCodexInvite_ParsesItemsAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[{"referral_id":"r9","email":"b@example.com","invite_url":"https://x/z"}],"cursor":null}`))
	}))
	defer server.Close()

	old := codexInviteURLForTest
	codexInviteURLForTest = server.URL
	defer func() { codexInviteURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123", AccountID: "acc-1"}
	res, err := SendCodexInvite(context.Background(), account, "", "", "", []string{"b@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if len(res.Invites) != 1 || res.Invites[0].ReferralID != "r9" {
		t.Fatalf("invites = %+v, want items[] parsed into invites", res.Invites)
	}
}

func TestSendCodexInvite_CustomProgram(t *testing.T) {
	var body struct {
		ProgramID  string `json:"program_id"`
		Entrypoint string `json:"entrypoint"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	old := codexInviteURLForTest
	codexInviteURLForTest = server.URL
	defer func() { codexInviteURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := SendCodexInvite(context.Background(), account, "", "codex_referral_business", "modal", []string{"a@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if body.ProgramID != "codex_referral_business" || body.Entrypoint != "modal" {
		t.Errorf("upstream body = %+v, want custom program/entrypoint", body)
	}
	if res.ProgramID != "codex_referral_business" || res.Entrypoint != "modal" {
		t.Errorf("result program = (%q,%q)", res.ProgramID, res.Entrypoint)
	}
}

func TestSendCodexInvite_403KeepsUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Referral invites are not available for your plan"}`))
	}))
	defer server.Close()

	old := codexInviteURLForTest
	codexInviteURLForTest = server.URL
	defer func() { codexInviteURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123", AccountID: "acc-1"}
	res, err := SendCodexInvite(context.Background(), account, "", "custom_program", "", []string{"a@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if res.OK || res.StatusCode != http.StatusForbidden {
		t.Fatalf("result = %+v, want not OK 403", res)
	}
	if res.ProgramID != "custom_program" || res.Entrypoint != DefaultEntrypoint {
		t.Errorf("program = (%q,%q), want (custom_program,%q)", res.ProgramID, res.Entrypoint, DefaultEntrypoint)
	}
	if len(res.Upstream) == 0 || !strings.Contains(string(res.Upstream), "not available") {
		t.Errorf("upstream not preserved: %s", res.Upstream)
	}
}

// 实测的收件人级拒绝：403 + detail 为对象。账号资格完好（send/reward 都有余额），
// 失败原因只是这个收件人已有有效邀请。必须把 message 和 failed_emails 透出来，
// 否则前端只看到 403，会误报成「该账号没有推荐邀请资格」。
func TestSendCodexInvite_ParsesRecipientRejection(t *testing.T) {
	const upstream = `{"detail":{"message":"此人已收到推荐邀请","failed_emails":["75950465@qq.com"]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(upstream))
	}))
	defer server.Close()

	old := codexInviteURLForTest
	codexInviteURLForTest = server.URL
	defer func() { codexInviteURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := SendCodexInvite(context.Background(), account, "", "", "", []string{"75950465@qq.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if res.OK || res.StatusCode != http.StatusForbidden {
		t.Fatalf("result = (ok=%v code=%d), want not ok 403", res.OK, res.StatusCode)
	}
	if res.UpstreamMessage != "此人已收到推荐邀请" {
		t.Errorf("upstream_message = %q, want the upstream reason", res.UpstreamMessage)
	}
	if len(res.FailedEmails) != 1 || res.FailedEmails[0] != "75950465@qq.com" {
		t.Errorf("failed_emails = %v, want the rejected recipient", res.FailedEmails)
	}
	// 收件人级拒绝不是 Cloudflare 挑战，也不该被当成挑战处理。
	if res.Challenged {
		t.Error("challenged = true, want false for a business rejection")
	}
}

// detail 为字符串的账号级无资格必须同样解析出来。
func TestSendCodexInvite_ParsesStringDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Referral invites are not available for your plan"}`))
	}))
	defer server.Close()

	old := codexInviteURLForTest
	codexInviteURLForTest = server.URL
	defer func() { codexInviteURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := SendCodexInvite(context.Background(), account, "", "", "", []string{"a@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if res.UpstreamMessage != "Referral invites are not available for your plan" {
		t.Errorf("upstream_message = %q, want the string detail", res.UpstreamMessage)
	}
	if len(res.FailedEmails) != 0 {
		t.Errorf("failed_emails = %v, want empty for a string detail", res.FailedEmails)
	}
}

func TestParseInviteFailureDetail_Shapes(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantMsg  string
		wantFail int
	}{
		{"string detail", `{"detail":"plan not supported"}`, "plan not supported", 0},
		{"object detail", `{"detail":{"message":"m","failed_emails":["a@b","c@d"]}}`, "m", 2},
		{"no detail", `{"other":1}`, "", 0},
		{"success body", `{"invites":[]}`, "", 0},
		{"detail null", `{"detail":null}`, "", 0},
		{"detail array", `{"detail":[{"msg":"x"}]}`, "", 0},
		{"not json", `<html></html>`, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, failed := parseInviteFailureDetail([]byte(tc.body))
			if msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", msg, tc.wantMsg)
			}
			if len(failed) != tc.wantFail {
				t.Errorf("failed_emails = %v, want %d entries", failed, tc.wantFail)
			}
		})
	}
}

func TestSendCodexInvite_NoToken(t *testing.T) {
	account := &auth.Account{DBID: 1}
	if _, err := SendCodexInvite(context.Background(), account, "", "", "", []string{"a@example.com"}); err == nil {
		t.Fatal("expected error when account has no access token")
	}
}

func TestSendCodexInvite_NoEmails(t *testing.T) {
	account := &auth.Account{DBID: 1, AccessToken: "at"}
	if _, err := SendCodexInvite(context.Background(), account, "", "", "", nil); err == nil {
		t.Fatal("expected error when no emails")
	}
}

func TestQueryCodexInviteEligibility_ParsesCapacity(t *testing.T) {
	var gotQuery, gotMethod string
	// 实测响应体（截取关键字段），含双层配额：send 10/月、reward 3/月。
	const upstream = `{"should_show":true,"ineligible_reason":null,"ineligible_reason_code":null,` +
		`"program_id":"codex_referral_consumer","entrypoint":"persistent","offer_id":"credits_1000",` +
		`"grants":[{"recipient":"referrer","grant_type":"personal_credits","amount":1000,"reward_id":"rid-a"},` +
		`{"recipient":"recipient","grant_type":"personal_credits","amount":1000,"reward_id":"rid-b"}],` +
		`"remaining_send_capacity":9,"remaining_reward_capacity":2,"title":"Get 1,000 credits","description":"desc",` +
		`"rules":["r1","r2"],"time_frame_rules":[{"invites_sent":1,"invites_total":10,"time_frame":"month","type":"user","capacity_type":"send"},` +
		`{"invites_sent":1,"invites_total":3,"time_frame":"month","type":"user","capacity_type":"reward"}],` +
		`"requires_explicit_confirmation":false}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotMethod = r.Method
		w.Header().Set("x-oai-request-id", "req-elig")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstream))
	}))
	defer server.Close()

	old := codexInviteEligibilityURLForTest
	codexInviteEligibilityURLForTest = server.URL
	defer func() { codexInviteEligibilityURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123", AccountID: "acc-1"}
	res, err := QueryCodexInviteEligibility(context.Background(), account, "", "", "")
	if err != nil {
		t.Fatalf("QueryCodexInviteEligibility error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if !strings.Contains(gotQuery, "program_id="+DefaultProgramID) || !strings.Contains(gotQuery, "entrypoint="+DefaultEntrypoint) {
		t.Errorf("query = %q, want default program/entrypoint", gotQuery)
	}
	if !res.OK || !res.ShouldShow || res.RequestID != "req-elig" {
		t.Fatalf("result = %+v", res)
	}
	if res.RemainingSendCapacity == nil || *res.RemainingSendCapacity != 9 {
		t.Errorf("remaining_send_capacity = %v, want 9", res.RemainingSendCapacity)
	}
	if res.RemainingRewardCapacity == nil || *res.RemainingRewardCapacity != 2 {
		t.Errorf("remaining_reward_capacity = %v, want 2", res.RemainingRewardCapacity)
	}
	if res.OfferID != "credits_1000" || len(res.Grants) != 2 || res.Grants[0].Amount != 1000 {
		t.Errorf("offer/grants = %q %+v", res.OfferID, res.Grants)
	}
	if len(res.TimeFrameRules) != 2 || res.TimeFrameRules[1].CapacityType != "reward" || res.TimeFrameRules[1].InvitesTotal != 3 {
		t.Errorf("time_frame_rules = %+v", res.TimeFrameRules)
	}
	if len(res.Rules) != 2 || res.Title == "" {
		t.Errorf("copy fields = %+v / %q", res.Rules, res.Title)
	}
}

// 上游没给 remaining_* 字段时必须留 nil，不能退化成 0 —— 0 会被前端当成「配额已用尽」。
func TestQueryCodexInviteEligibility_MissingCapacityStaysNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"should_show":true}`))
	}))
	defer server.Close()

	old := codexInviteEligibilityURLForTest
	codexInviteEligibilityURLForTest = server.URL
	defer func() { codexInviteEligibilityURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := QueryCodexInviteEligibility(context.Background(), account, "", "", "")
	if err != nil {
		t.Fatalf("QueryCodexInviteEligibility error: %v", err)
	}
	if res.RemainingSendCapacity != nil || res.RemainingRewardCapacity != nil {
		t.Fatalf("capacity = (%v,%v), want both nil", res.RemainingSendCapacity, res.RemainingRewardCapacity)
	}
	// 上游未回显 program_id/entrypoint 时保留请求参数，不能被清空。
	if res.ProgramID != DefaultProgramID || res.Entrypoint != DefaultEntrypoint {
		t.Errorf("program = (%q,%q), want request values preserved", res.ProgramID, res.Entrypoint)
	}
}

func TestQueryCodexInviteEligibility_403KeepsUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"not eligible"}`))
	}))
	defer server.Close()

	old := codexInviteEligibilityURLForTest
	codexInviteEligibilityURLForTest = server.URL
	defer func() { codexInviteEligibilityURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := QueryCodexInviteEligibility(context.Background(), account, "", "", "")
	if err != nil {
		t.Fatalf("QueryCodexInviteEligibility error: %v", err)
	}
	if res.OK || res.StatusCode != http.StatusForbidden {
		t.Fatalf("result = %+v, want not OK 403", res)
	}
	if !strings.Contains(string(res.Upstream), "not eligible") {
		t.Errorf("upstream not preserved: %s", res.Upstream)
	}
}

func TestQueryCodexInviteTracking_ParsesItems(t *testing.T) {
	var gotQuery string
	const upstream = `{"items":[{"referral_id":"6a70","email":"a@example.com","status":"redeemed","can_resend":false,` +
		`"invite_url":null,"resend_available_at":null,"grants":[{"recipient":"referrer","grant_type":"personal_credits","amount":1000}],` +
		`"created_at":"2026-08-03T05:24:58.842913Z","expires_at":"2026-08-10T05:24:58.842913Z"}],"cursor":null}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstream))
	}))
	defer server.Close()

	old := codexInviteTrackingURLForTest
	codexInviteTrackingURLForTest = server.URL
	defer func() { codexInviteTrackingURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := QueryCodexInviteTracking(context.Background(), account, "", "", "", 0)
	if err != nil {
		t.Fatalf("QueryCodexInviteTracking error: %v", err)
	}
	if !strings.Contains(gotQuery, "period="+defaultTrackingPeriod) || !strings.Contains(gotQuery, "limit=100") {
		t.Errorf("query = %q, want official defaults", gotQuery)
	}
	if !res.OK || len(res.Items) != 1 {
		t.Fatalf("result = %+v", res)
	}
	item := res.Items[0]
	if item.ReferralID != "6a70" || item.Status != "redeemed" || item.CanResend {
		t.Errorf("item = %+v", item)
	}
	// invite_url / resend_available_at 上游为 null，解析后应为空串而非报错。
	if item.InviteURL != "" || item.ResendAvailableAt != "" {
		t.Errorf("null fields = %q %q, want empty", item.InviteURL, item.ResendAvailableAt)
	}
	if item.ExpiresAt == "" || len(item.Grants) != 1 {
		t.Errorf("item detail = %+v", item)
	}
}

// 超限或非正的 limit 一律夹到官方默认值，避免上游 422。
func TestQueryCodexInviteTracking_ClampsLimit(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[],"cursor":null}`))
	}))
	defer server.Close()

	old := codexInviteTrackingURLForTest
	codexInviteTrackingURLForTest = server.URL
	defer func() { codexInviteTrackingURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	if _, err := QueryCodexInviteTracking(context.Background(), account, "", "", "past_30_days", 9999); err != nil {
		t.Fatalf("QueryCodexInviteTracking error: %v", err)
	}
	if !strings.Contains(gotQuery, "limit=100") || !strings.Contains(gotQuery, "period=past_30_days") {
		t.Errorf("query = %q, want clamped limit and custom period", gotQuery)
	}
}

// Cloudflare 的 managed challenge 借用 403 状态码返回挑战页 HTML。这种响应必须标成
// Challenged 而不是当成「上游判定无资格」，否则用户会被告知账号没有推荐权限——实测同一账号
// 连续请求会在 200 和挑战 403 之间来回跳，按状态码解读就是纯误报。
const cfChallengeBody = `<html><head><style global>body{}</style></head><body>` +
	`<script>(function(){window._cf_chl_opt = {cRay: 'a253ab385a765121'};` +
	`var a = document.createElement('script');a.src = '/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1';}());</script>` +
	`</body></html>`

func TestQueryCodexInviteEligibility_DetectsCloudflareChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(cfChallengeBody))
	}))
	defer server.Close()

	old := codexInviteEligibilityURLForTest
	codexInviteEligibilityURLForTest = server.URL
	defer func() { codexInviteEligibilityURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := QueryCodexInviteEligibility(context.Background(), account, "", "", "")
	if err != nil {
		t.Fatalf("QueryCodexInviteEligibility error: %v", err)
	}
	if !res.Challenged {
		t.Fatal("challenged = false, want true for a Cloudflare challenge page")
	}
	// 挑战页不代表无资格：should_show 不能被置成有意义的值，HTML 也不该回给前端。
	if res.ShouldShow {
		t.Error("should_show = true, want false when challenged")
	}
	if strings.Contains(res.UpstreamRaw, "<html") || len(res.Upstream) > 0 {
		t.Errorf("challenge HTML leaked into result: raw=%q upstream=%d bytes", res.UpstreamRaw, len(res.Upstream))
	}
	if res.UpstreamRaw != cloudflareChallengeMarker {
		t.Errorf("upstream_raw = %q, want challenge marker", res.UpstreamRaw)
	}
}

func TestSendCodexInvite_DetectsCloudflareChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(cfChallengeBody))
	}))
	defer server.Close()

	old := codexInviteURLForTest
	codexInviteURLForTest = server.URL
	defer func() { codexInviteURLForTest = old }()

	account := &auth.Account{DBID: 1, AccessToken: "at-123"}
	res, err := SendCodexInvite(context.Background(), account, "", "", "", []string{"a@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if !res.Challenged || res.OK {
		t.Fatalf("result = (challenged=%v ok=%v), want challenged and not ok", res.Challenged, res.OK)
	}
	if res.UpstreamRaw != cloudflareChallengeMarker {
		t.Errorf("upstream_raw = %q, want challenge marker", res.UpstreamRaw)
	}
}

// 真正的业务 403（JSON detail）不能被误标成 Cloudflare 挑战。
func TestInviteChallengeDetection_IgnoresBusiness403(t *testing.T) {
	if isCloudflareChallenge(http.StatusForbidden, []byte(`{"detail":"not eligible"}`)) {
		t.Error("business 403 JSON misdetected as a Cloudflare challenge")
	}
	if isCloudflareChallenge(http.StatusOK, []byte(cfChallengeBody)) {
		t.Error("200 response misdetected as a challenge")
	}
	if !isCloudflareChallenge(http.StatusTooManyRequests, []byte(cfChallengeBody)) {
		t.Error("429 challenge page not detected")
	}
}

// 真实挑战页把 ~7KB 的内联 SVG 图标放在挑战脚本之前，特征标记远在文档开头之外。
// 只扫正文头部会漏判，403 就会被当成「无资格」上报给用户。
func TestInviteChallengeDetection_MarkerBeyondHeadOfBody(t *testing.T) {
	// 用与真实挑战页同构的体积：先一大段 SVG path，再出现 cf_chl_opt。
	padding := strings.Repeat("M37.5324 16.8707C37.9808 15.5241 38.1363 14.0974 ", 200)
	body := []byte(`<html><head><style global>body{}</style></head><body><svg><path d="` + padding +
		`" /></svg><script>(function(){window._cf_chl_opt = {cRay: 'abc'};}());</script></body></html>`)
	if len(body) < 8192 {
		t.Fatalf("test fixture too small (%d bytes) to cover the regression", len(body))
	}
	if !isCloudflareChallenge(http.StatusForbidden, body) {
		t.Error("challenge page with marker past the head of the body not detected")
	}
}

// cookie jar 按账号隔离，避免把一个账号的 oai-sc 串到另一个账号的请求上。
func TestInviteCookieJarIsolatedPerAccount(t *testing.T) {
	a := &auth.Account{DBID: 1, AccountID: "acc-1"}
	b := &auth.Account{DBID: 2, AccountID: "acc-2"}
	jarA, jarA2, jarB := inviteCookieJarFor(a), inviteCookieJarFor(a), inviteCookieJarFor(b)
	if jarA == nil || jarB == nil {
		t.Fatal("cookie jar is nil")
	}
	if jarA != jarA2 {
		t.Error("same account got two different jars, cookies would not be reused")
	}
	if jarA == jarB {
		t.Error("different accounts share a jar, session cookies could bleed across accounts")
	}
}

func TestQueryCodexInviteTracking_NoToken(t *testing.T) {
	account := &auth.Account{DBID: 1}
	if _, err := QueryCodexInviteTracking(context.Background(), account, "", "", "", 0); err == nil {
		t.Fatal("expected error when account has no access token")
	}
}
