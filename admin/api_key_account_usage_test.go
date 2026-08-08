package admin

import (
	"math"
	"testing"

	"github.com/codex2api/database"
)

func TestAggregateAPIKeyAccountGroups(t *testing.T) {
	items := []database.APIKeyAccountStat{
		{
			AccountID: 1, Requests: 2, TotalTokens: 100, AccountBilled: 0.2, UserBilled: 0.3,
			Groups: []database.APIKeyAccountGroup{{ID: 10, Name: "primary", Color: "#112233"}},
		},
		{
			AccountID: 2, Requests: 3, TotalTokens: 200, AccountBilled: 0.4, UserBilled: 0.6,
			Groups: []database.APIKeyAccountGroup{{ID: 10, Name: "primary"}, {ID: 20, Name: "shared"}},
		},
	}

	groups, summary := aggregateAPIKeyAccountGroups(items)
	if summary.Accounts != 2 || summary.Requests != 5 || summary.TotalTokens != 300 || math.Abs(summary.AccountBilled-0.6) > 1e-9 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2", groups)
	}
	if groups[0].ID != 10 || groups[0].Accounts != 2 || groups[0].Requests != 5 || math.Abs(groups[0].UserBilled-0.9) > 1e-9 {
		t.Fatalf("primary group = %+v", groups[0])
	}
	if groups[1].ID != 20 || groups[1].Accounts != 1 || groups[1].TotalTokens != 200 {
		t.Fatalf("shared group = %+v", groups[1])
	}
}
