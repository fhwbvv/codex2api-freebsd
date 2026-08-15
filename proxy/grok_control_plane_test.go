package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/auth"
)

func TestFetchGrokControlPlaneFactKeepsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/settings" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"access_denied"}}`)
	}))
	defer server.Close()
	account := &auth.Account{UpstreamType: auth.UpstreamGrok, APIKey: "xai", BaseURL: server.URL + "/v1"}
	result, err := FetchGrokControlPlaneFact(context.Background(), account, "", GrokControlPlaneSettings, "")
	if err != nil {
		t.Fatalf("HTTP fact must not become Go error: %v", err)
	}
	if result.StatusCode != http.StatusForbidden || len(result.Body) == 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestFetchGrokControlPlaneFactKeepsStatusButDropsNonJSONErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `<html><body>private edge diagnostic</body></html>`)
	}))
	defer server.Close()

	account := &auth.Account{UpstreamType: auth.UpstreamGrok, APIKey: "xai", BaseURL: server.URL + "/v1"}
	result, err := FetchGrokControlPlaneFact(context.Background(), account, "", GrokControlPlaneUser, "")
	if err != nil {
		t.Fatalf("non-JSON HTTP failure must retain its status: %v", err)
	}
	if result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusUnauthorized)
	}
	if len(result.Body) != 0 {
		t.Fatalf("non-JSON error body was retained: %q", result.Body)
	}
}

func TestFetchGrokControlPlaneFactRejectsNonJSONSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "unexpected success body")
	}))
	defer server.Close()

	account := &auth.Account{UpstreamType: auth.UpstreamGrok, APIKey: "xai", BaseURL: server.URL + "/v1"}
	if _, err := FetchGrokControlPlaneFact(context.Background(), account, "", GrokControlPlaneSettings, ""); err == nil {
		t.Fatal("non-JSON success was accepted as a valid fact")
	}
}

func TestGrokControlPlaneHeadersAreEndpointSpecific(t *testing.T) {
	seen := map[string]http.Header{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	account := &auth.Account{
		UpstreamType: auth.UpstreamGrok, AccessToken: "at", AccountID: "user-1",
		Email: "private@example.test", BaseURL: server.URL + "/v1",
	}
	for _, kind := range []GrokControlPlaneFactKind{GrokControlPlaneUser, GrokControlPlaneSettings, GrokControlPlaneBilling, GrokControlPlaneAutoTopup} {
		if _, err := FetchGrokControlPlaneFact(context.Background(), account, "", kind, ""); err != nil {
			t.Fatal(err)
		}
	}
	for path, header := range seen {
		if header.Get("x-grok-session-id") != "" || header.Get("x-compaction-at") != "" || header.Get("Content-Type") != "" {
			t.Fatalf("%s inherited inference headers: %#v", path, header)
		}
	}
	if seen["/v1/user"].Get("x-userid") != "" {
		t.Fatal("/user must not send x-userid")
	}
	if seen["/v1/settings"].Get("x-grok-client-identifier") == "" {
		t.Fatal("/settings missing client identifier")
	}
	if seen["/v1/settings"].Get("x-email") != "private@example.test" {
		t.Fatalf("/settings x-email = %q", seen["/v1/settings"].Get("x-email"))
	}
	if seen["/v1/user"].Get("x-email") != "" || seen["/v1/billing"].Get("x-email") != "" || seen["/v1/auto-topup-rule"].Get("x-email") != "" {
		t.Fatal("x-email must remain scoped to settings")
	}
	for _, path := range []string{"/v1/billing", "/v1/auto-topup-rule"} {
		if seen[path].Get("x-userid") != "user-1" || seen[path].Get("x-grok-client-identifier") != "" {
			t.Fatalf("wrong %s headers: %#v", path, seen[path])
		}
	}
}
