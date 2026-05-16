package database

import (
	"math"
	"testing"
)

func TestGetModelPricingUsesMostSpecificOpenAIPrefix(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{model: "gpt-4o-mini-2024-07-18", wantInput: 0.15, wantOutput: 0.6},
		{model: "gpt-4o-2024-08-06", wantInput: 2.5, wantOutput: 10.0},
		{model: "gpt-4-0613", wantInput: 30.0, wantOutput: 60.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := getModelPricing(tt.model)
			assertPricing(t, got, tt.wantInput, tt.wantOutput)
		})
	}
}

func TestGetModelPricingUsesSub2APICodexFallbacks(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{model: "gpt-5.4-mini-20260401", wantInput: 0.75, wantOutput: 4.5},
		{model: "gpt-5.3-codex-spark", wantInput: 1.25, wantOutput: 10.0},
		{model: "gpt-5.3-codex", wantInput: 1.75, wantOutput: 14.0},
		{model: "gpt-5.5", wantInput: 2.5, wantOutput: 15.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := getModelPricing(tt.model)
			assertPricing(t, got, tt.wantInput, tt.wantOutput)
		})
	}
}

func TestGetModelPricingUsesSub2APIClaudeFamilies(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{model: "claude-opus-4-7-20260401", wantInput: 5.0, wantOutput: 25.0},
		{model: "claude-opus-4-20250514", wantInput: 15.0, wantOutput: 75.0},
		{model: "claude-sonnet-4-5-20250929", wantInput: 3.0, wantOutput: 15.0},
		{model: "claude-3-5-haiku-20241022", wantInput: 1.0, wantOutput: 5.0},
		{model: "claude-unknown-model", wantInput: 3.0, wantOutput: 15.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := getModelPricing(tt.model)
			assertPricing(t, got, tt.wantInput, tt.wantOutput)
		})
	}
}

func TestCalculateCostHandlesCachedTokensAndServiceTier(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		serviceTier  string
		inputTokens  int
		outputTokens int
		cachedTokens int
		want         float64
	}{
		{
			name:         "discounts cached tokens when cache pricing exists",
			model:        "gpt-5.4",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.00955,
		},
		{
			name:         "keeps legacy input price when cache pricing is unavailable",
			model:        "gpt-4o",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.0075,
		},
		{
			name:         "uses priority prices when available",
			model:        "gpt-5.4",
			serviceTier:  "priority",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.0191,
		},
		{
			name:         "applies flex multiplier",
			model:        "gpt-5.4",
			serviceTier:  "flex",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.004775,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateCost(tt.inputTokens, tt.outputTokens, tt.cachedTokens, tt.model, tt.serviceTier)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("calculateCost() = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestCalculateCostBreakdownExposesDisplayFields(t *testing.T) {
	got := calculateCostBreakdown(1000, 500, 200, "gpt-5.4", "flex")

	assertFloatEqual(t, got.InputCost, 0.001)
	assertFloatEqual(t, got.CacheReadCost, 0.000025)
	assertFloatEqual(t, got.OutputCost, 0.00375)
	assertFloatEqual(t, got.TotalCost, 0.004775)
	assertFloatEqual(t, got.InputPricePerMToken, 1.25)
	assertFloatEqual(t, got.CacheReadPricePerMToken, 0.125)
	assertFloatEqual(t, got.OutputPricePerMToken, 7.5)
	assertFloatEqual(t, got.ServiceTierCostMultiplier, 0.5)
}

func TestGPT55PricingMatchesGPT54Fallback(t *testing.T) {
	gpt54 := getModelPricing("gpt-5.4")
	gpt55 := getModelPricing("gpt-5.5")

	assertFloatEqual(t, gpt55.InputPricePerMToken, gpt54.InputPricePerMToken)
	assertFloatEqual(t, gpt55.OutputPricePerMToken, gpt54.OutputPricePerMToken)
	assertFloatEqual(t, gpt55.CacheReadPricePerMToken, gpt54.CacheReadPricePerMToken)
	assertFloatEqual(t, gpt55.InputPricePerMTokenPriority, gpt54.InputPricePerMTokenPriority)
	assertFloatEqual(t, gpt55.OutputPricePerMTokenPriority, gpt54.OutputPricePerMTokenPriority)
	assertFloatEqual(t, gpt55.CacheReadPricePerMTokenPriority, gpt54.CacheReadPricePerMTokenPriority)
}

func TestSparkPricingUsesGpt51CodexFallback(t *testing.T) {
	spark := getModelPricing("gpt-5.3-codex-spark-high")

	assertFloatEqual(t, spark.InputPricePerMToken, 1.25)
	assertFloatEqual(t, spark.OutputPricePerMToken, 10.0)
	assertFloatEqual(t, spark.CacheReadPricePerMToken, 0.125)
	assertFloatEqual(t, spark.InputPricePerMTokenPriority, 2.5)
	assertFloatEqual(t, spark.OutputPricePerMTokenPriority, 20.0)
	assertFloatEqual(t, spark.CacheReadPricePerMTokenPriority, 0.25)
}

func TestGPT53CodexPricingUsesGPT52CodexFallback(t *testing.T) {
	codex := getModelPricing("gpt-5.3-codex-xhigh")
	gpt52 := getModelPricing("gpt-5.2")

	assertFloatEqual(t, codex.InputPricePerMToken, gpt52.InputPricePerMToken)
	assertFloatEqual(t, codex.OutputPricePerMToken, gpt52.OutputPricePerMToken)
	assertFloatEqual(t, codex.CacheReadPricePerMToken, gpt52.CacheReadPricePerMToken)
	assertFloatEqual(t, codex.InputPricePerMTokenPriority, gpt52.InputPricePerMTokenPriority)
	assertFloatEqual(t, codex.OutputPricePerMTokenPriority, gpt52.OutputPricePerMTokenPriority)
	assertFloatEqual(t, codex.CacheReadPricePerMTokenPriority, gpt52.CacheReadPricePerMTokenPriority)
}

func TestUsageLogBreakdownScalesToStoredBilledTotal(t *testing.T) {
	log := &UsageLog{
		Model:         "gpt-5.5",
		InputTokens:   1000,
		StatusCode:    200,
		AccountBilled: 0.0025,
		UserBilled:    0.0025,
	}

	log.populateBillingBreakdown()

	assertFloatEqual(t, log.TotalCost, 0.0025)
	assertFloatEqual(t, log.InputCost, 0.0025)
	assertFloatEqual(t, log.InputPrice, 2.5)
}

func assertPricing(t *testing.T, got *ModelPricing, wantInput, wantOutput float64) {
	t.Helper()
	if got == nil {
		t.Fatal("getModelPricing returned nil")
	}
	if math.Abs(got.InputPricePerMToken-wantInput) > 1e-12 || math.Abs(got.OutputPricePerMToken-wantOutput) > 1e-12 {
		t.Fatalf("pricing = input %.12f output %.12f, want input %.12f output %.12f",
			got.InputPricePerMToken, got.OutputPricePerMToken, wantInput, wantOutput)
	}
}

func assertFloatEqual(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}
