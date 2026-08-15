package proxy

import (
	"testing"
	"time"

	"github.com/codex2api/auth"
)

func TestApplyGrokBillingDoesNotInferPlanOrQuotaWindows(t *testing.T) {
	weekly, monthly := 100.0, 75.0
	account := &auth.Account{PlanType: "archive-label"}
	credentials := ApplyGrokBilling(nil, account, &GrokBillingSummary{
		Plan: "supergrok", WeeklyPercent: &weekly, MonthlyPercent: &monthly,
		WeeklyPeriodEnd:  time.Now().Add(time.Hour).Format(time.RFC3339),
		MonthlyPeriodEnd: time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
	if got := account.GetPlanType(); got != "archive-label" {
		t.Fatalf("plan_type mutated from billing: %q", got)
	}
	if _, exists := credentials["plan_type"]; exists {
		t.Fatal("billing credentials must not contain plan_type")
	}
	if _, _, ok := account.GetUsageSnapshot5h(); ok {
		t.Fatal("weekly billing was copied into the generic 5h window")
	}
	if _, ok := account.GetUsagePercent7d(); ok {
		t.Fatal("monthly billing was copied into the generic 7d window")
	}
	if _, ok := credentials["grok_billing_detail"]; !ok {
		t.Fatal("legacy grok_billing_detail must still be dual-written")
	}
}
