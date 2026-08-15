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
		{
			AccountID: 3, Requests: 1, TotalTokens: 50, AccountBilled: 0.1, UserBilled: 0.15,
		},
	}

	groups, summary, reconciliation := aggregateAPIKeyAccountGroups(items)
	if summary.Accounts != 3 || summary.Requests != 6 || summary.TotalTokens != 350 || math.Abs(summary.AccountBilled-0.7) > 1e-9 || math.Abs(summary.UserBilled-1.05) > 1e-9 {
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
	if reconciliation.UniqueGroupedAccounts != 2 || reconciliation.MultiGroupAccounts != 1 {
		t.Fatalf("reconciliation account counts = %+v", reconciliation)
	}
	if reconciliation.GroupedTotal.Accounts != 3 || reconciliation.GroupedTotal.Requests != 8 || reconciliation.GroupedTotal.TotalTokens != 500 || math.Abs(reconciliation.GroupedTotal.UserBilled-1.5) > 1e-9 {
		t.Fatalf("grouped total = %+v", reconciliation.GroupedTotal)
	}
	if reconciliation.Ungrouped.Accounts != 1 || reconciliation.Ungrouped.TotalTokens != 50 || math.Abs(reconciliation.Ungrouped.UserBilled-0.15) > 1e-9 {
		t.Fatalf("ungrouped = %+v", reconciliation.Ungrouped)
	}
	if reconciliation.Duplicate.Accounts != 1 || reconciliation.Duplicate.Requests != 3 || reconciliation.Duplicate.TotalTokens != 200 || math.Abs(reconciliation.Duplicate.UserBilled-0.6) > 1e-9 {
		t.Fatalf("duplicate = %+v", reconciliation.Duplicate)
	}
	reconciled := reconciliation.GroupedTotal.UserBilled + reconciliation.Ungrouped.UserBilled - reconciliation.Duplicate.UserBilled
	if math.Abs(reconciled-summary.UserBilled) > 1e-9 {
		t.Fatalf("reconciled billed = %f, summary = %f", reconciled, summary.UserBilled)
	}
}
