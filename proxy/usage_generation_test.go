package proxy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
)

func TestPopulateUsageCredentialGenerationUsesDispatchedGrokAccount(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 2})
	store.AddAccount(&auth.Account{
		DBID: 1, UpstreamType: auth.UpstreamGrok, APIKey: "xai-first", CredentialGeneration: 2,
	})
	store.AddAccount(&auth.Account{
		DBID: 2, UpstreamType: auth.UpstreamOpenAIResponses, APIKey: "relay", CredentialGeneration: 9,
	})
	store.AddAccount(&auth.Account{
		DBID: 3, UpstreamType: auth.UpstreamGrok, APIKey: "xai-retry", CredentialGeneration: 7,
	})
	handler := &Handler{store: store}

	tests := []struct {
		name      string
		accountID int64
		explicit  int64
		want      int64
	}{
		{name: "grok initial attempt", accountID: 1, want: 2},
		{name: "non grok", accountID: 2, want: 0},
		{name: "grok replacement account", accountID: 3, want: 7},
		{name: "missing account", accountID: 404, want: 0},
		{name: "no account", accountID: 0, want: 0},
		{name: "explicit grok request snapshot", accountID: 1, explicit: 11, want: 11},
		{name: "explicit non grok value", accountID: 2, explicit: 13, want: 13},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := &database.UsageLogInput{AccountID: test.accountID, CredentialGeneration: test.explicit}
			handler.populateUsageCredentialGeneration(input)
			if input.CredentialGeneration != test.want {
				t.Fatalf("credential generation = %d, want %d", input.CredentialGeneration, test.want)
			}
		})
	}
}

func TestLogUsagePersistsGrokCredentialGeneration(t *testing.T) {
	ctx := context.Background()
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "usage-generation.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	accountID, err := db.InsertAccountWithUpstream(ctx, "grok-generation", "xai", "grok", map[string]interface{}{
		"upstream_type": "grok",
		"api_key":       "first-key",
	}, "")
	if err != nil {
		t.Fatalf("insert Grok account: %v", err)
	}
	row, err := db.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatalf("get Grok account: %v", err)
	}
	store := auth.NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 1})
	store.AddAccount(&auth.Account{
		DBID: accountID, UpstreamType: auth.UpstreamGrok, APIKey: "first-key",
		CredentialGeneration: row.CredentialGeneration,
	})
	handler := &Handler{store: store, db: db}
	startedAt := time.Now().Add(-time.Second)
	input := &database.UsageLogInput{
		AccountID: accountID, Endpoint: "/v1/responses", Model: "grok-4.5",
		StatusCode: 200, TotalTokens: 17, IsRetryAttempt: true, AttemptIndex: 1,
	}
	handler.logUsage(input)
	db.FlushUsageLogs()
	if input.CredentialGeneration != row.CredentialGeneration {
		t.Fatalf("buffered credential generation = %d, want %d", input.CredentialGeneration, row.CredentialGeneration)
	}

	// Rotate the persisted identity. A correctly scoped old-generation row is
	// now excluded; a legacy zero-generation row would incorrectly remain.
	if err := db.UpdateCredentials(ctx, accountID, map[string]interface{}{"api_key": "rotated-key"}); err != nil {
		t.Fatalf("rotate Grok identity: %v", err)
	}
	usage, err := db.GetAccountTimeRangeUsage(ctx, startedAt)
	if err != nil {
		t.Fatalf("GetAccountTimeRangeUsage: %v", err)
	}
	if got := usage[accountID]; got != nil {
		t.Fatalf("stale generation usage remained visible after rotation: %+v", got)
	}
}
