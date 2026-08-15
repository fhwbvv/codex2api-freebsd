package proxy

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/database"
)

// This opt-in test performs a real official-source fetch but writes only to a
// temporary SQLite database. It is intentionally excluded from ordinary test
// runs so CI never depends on external network availability.
func TestLiveOfficialPricingSyncIsolated(t *testing.T) {
	if os.Getenv("CODEX_LIVE_OFFICIAL_PRICING_TEST") != "1" {
		t.Skip("set CODEX_LIVE_OFFICIAL_PRICING_TEST=1 to run live official pricing verification")
	}
	database.SetModelPricingOverrides(nil)
	t.Cleanup(func() { database.SetModelPricingOverrides(nil) })

	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "official-pricing-live.db"))
	if err != nil {
		t.Fatalf("create isolated sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	_, err = db.MutateModelPricingSettings(ctx, nil, func(current map[string]database.ModelPricingOverride) error {
		current["gpt-5.4"] = database.ModelPricingOverride{
			Source: database.ModelPricingSourceCustom,
			Input:  9.99,
			Output: 123,
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed custom pricing: %v", err)
	}

	beforeLogs, err := db.ListRecentUsageLogs(ctx, 10)
	if err != nil || len(beforeLogs) != 0 {
		t.Fatalf("isolated database should start without usage logs: count=%d err=%v", len(beforeLogs), err)
	}

	result, err := SyncOfficialModelPricing(ctx, db, "", OfficialPricingSyncOptions{
		Models: []string{
			"gpt-5.6-sol",
			"gpt-5.6-terra",
			"gpt-5.6-luna",
			"gpt-5.5",
			"gpt-5.4",
			"grok-4.6",
		},
		IncludeOpenAI: true,
		IncludeGrok:   true,
	})
	if err != nil {
		t.Fatalf("live official pricing sync: %v", err)
	}
	if result.Fetched != 6 || result.Applied != 5 || result.Skipped != 1 {
		t.Fatalf("unexpected live sync result: %+v", result)
	}
	if len(result.Warnings) != 0 || len(result.Missing) != 0 {
		t.Fatalf("official sources should cover the selected models: %+v", result)
	}

	settings, err := db.GetSystemSettings(ctx)
	if err != nil {
		t.Fatalf("read isolated settings: %v", err)
	}
	overrides, err := database.ParseModelPricingOverridesJSON(settings.ModelPricingOverrides)
	if err != nil {
		t.Fatalf("parse isolated overrides: %v", err)
	}
	custom := overrides["gpt-5.4"]
	if custom.Source != database.ModelPricingSourceCustom || custom.Input != 9.99 || custom.Output != 123 {
		t.Fatalf("custom price was overwritten: %+v", custom)
	}

	assertRates := func(model string, got database.ModelPricingOverride, want [12]float64) {
		t.Helper()
		actual := [12]float64{
			got.Input, got.CachedInput, got.Output,
			got.InputPriority, got.CachedInputPriority, got.OutputPriority,
			got.InputLong, got.CachedInputLong, got.OutputLong,
			got.InputLongPriority, got.CachedInputLongPriority, got.OutputLongPriority,
		}
		if actual != want {
			t.Fatalf("%s official rates = %v, want %v", model, actual, want)
		}
	}
	assertRates("gpt-5.6-sol", overrides["gpt-5.6-sol"], [12]float64{5, 0.5, 30, 10, 1, 60, 10, 1, 45, 20, 2, 90})
	assertRates("gpt-5.6-terra", overrides["gpt-5.6-terra"], [12]float64{2, 0.2, 12, 4, 0.4, 24, 4, 0.4, 18, 8, 0.8, 36})
	assertRates("gpt-5.6-luna", overrides["gpt-5.6-luna"], [12]float64{0.2, 0.02, 1.2, 0.4, 0.04, 2.4, 0.4, 0.04, 1.8, 0.8, 0.08, 3.6})
	assertRates("grok-4.6", overrides["grok-4.6"], [12]float64{2, 0.5, 6, 0, 0, 0, 4, 1, 12, 0, 0, 0})

	assertCost := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 0.000001 {
			t.Fatalf("%s cost = %.8f, want %.8f", name, got, want)
		}
	}
	assertCost("Codex standard", database.CalculateCost(100_000, 1_000_000, 0, "gpt-5.6-sol", "standard"), 30.5)
	assertCost("Codex Fast", database.CalculateCost(100_000, 1_000_000, 0, "gpt-5.6-sol", "fast"), 61)
	assertCost("Codex long Fast", database.CalculateCost(1_000_000, 1_000_000, 0, "gpt-5.6-sol", "fast"), 110)
	assertCost("Codex cached Fast", database.CalculateCost(100_000, 0, 100_000, "gpt-5.6-sol", "fast"), 0.1)
	assertCost("Grok standard", database.CalculateCost(100_000, 1_000_000, 0, "grok-4.6", "standard"), 6.2)
	grokLong := database.CalculateCostBreakdown(200_001, 1_000_000, 0, "grok-4.6", "standard")
	if !grokLong.LongContext || grokLong.LongContextThreshold != 200000 {
		t.Fatalf("Grok long-context threshold not applied: %+v", grokLong)
	}
	assertCost("Grok long", grokLong.TotalCost, 12.800004)

	afterLogs, err := db.ListRecentUsageLogs(ctx, 10)
	if err != nil || len(afterLogs) != 0 {
		t.Fatalf("pricing sync must not create usage logs: count=%d err=%v", len(afterLogs), err)
	}
}
