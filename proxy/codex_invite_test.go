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
		ReferralKey string   `json:"referral_key"`
		Emails      []string `json:"emails"`
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
	res, err := SendCodexInvite(context.Background(), account, "", "", []string{"a@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if !res.OK || res.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, want OK 200", res)
	}
	if res.RequestID != "req-123" {
		t.Errorf("request_id = %q, want req-123", res.RequestID)
	}
	if res.ReferralKey != DefaultReferralKey {
		t.Errorf("referral_key = %q, want default %q", res.ReferralKey, DefaultReferralKey)
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
	if body.ReferralKey != DefaultReferralKey || len(body.Emails) != 1 {
		t.Errorf("upstream body = %+v", body)
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
	res, err := SendCodexInvite(context.Background(), account, "", "custom_key", []string{"a@example.com"})
	if err != nil {
		t.Fatalf("SendCodexInvite error: %v", err)
	}
	if res.OK || res.StatusCode != http.StatusForbidden {
		t.Fatalf("result = %+v, want not OK 403", res)
	}
	if res.ReferralKey != "custom_key" {
		t.Errorf("referral_key = %q, want custom_key", res.ReferralKey)
	}
	if len(res.Upstream) == 0 || !strings.Contains(string(res.Upstream), "not available") {
		t.Errorf("upstream not preserved: %s", res.Upstream)
	}
}

func TestSendCodexInvite_NoToken(t *testing.T) {
	account := &auth.Account{DBID: 1}
	if _, err := SendCodexInvite(context.Background(), account, "", "", []string{"a@example.com"}); err == nil {
		t.Fatal("expected error when account has no access token")
	}
}

func TestSendCodexInvite_NoEmails(t *testing.T) {
	account := &auth.Account{DBID: 1, AccessToken: "at"}
	if _, err := SendCodexInvite(context.Background(), account, "", "", nil); err == nil {
		t.Fatal("expected error when no emails")
	}
}
