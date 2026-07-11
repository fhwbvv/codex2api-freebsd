package database

import (
	"testing"
)

func TestModelPricingOverride_MergeAndPrecedence(t *testing.T) {
	t.Cleanup(func() { SetModelPricingOverrides(nil) })

	// 基线：gpt-5.4 代码默认 $2.5/$15。
	base := GetModelPricing("gpt-5.4")
	if base.InputPricePerMToken != 2.5 || base.OutputPricePerMToken != 15.0 {
		t.Fatalf("baseline gpt-5.4 = %.2f/%.2f, want 2.5/15", base.InputPricePerMToken, base.OutputPricePerMToken)
	}

	// 部分覆盖：只改 output，input 应保持代码默认。
	SetModelPricingOverrides(map[string]ModelPricingOverride{
		"gpt-5.4": {Source: ModelPricingSourceCustom, Output: 99.0},
	})
	p := GetModelPricing("gpt-5.4")
	if p.OutputPricePerMToken != 99.0 {
		t.Fatalf("overridden output = %.2f, want 99", p.OutputPricePerMToken)
	}
	if p.InputPricePerMToken != 2.5 {
		t.Fatalf("input should stay code default 2.5, got %.2f", p.InputPricePerMToken)
	}
	// 覆盖不能污染共享默认：再查一次未覆盖模型仍为原值。
	if base2 := GetModelPricing("gpt-5.5"); base2.OutputPricePerMToken != 30.0 {
		t.Fatalf("gpt-5.5 leaked to %.2f, want 30", base2.OutputPricePerMToken)
	}

	// gpt-5.6-terra 是独立规范键：不跟随 gpt-5.4 的 custom 覆盖。
	if terra := GetModelPricing("gpt-5.6-terra"); terra.OutputPricePerMToken != 15.0 {
		t.Fatalf("terra should keep own default 15, got %.2f", terra.OutputPricePerMToken)
	}
	// terra 自身覆盖可独立生效。
	SetModelPricingOverrides(map[string]ModelPricingOverride{
		"gpt-5.4":       {Source: ModelPricingSourceCustom, Output: 99.0},
		"gpt-5.6-terra": {Source: ModelPricingSourceCustom, Output: 42.0},
	})
	if terra := GetModelPricing("gpt-5.6-terra"); terra.OutputPricePerMToken != 42.0 {
		t.Fatalf("terra override = %.2f, want 42", terra.OutputPricePerMToken)
	}
	if gpt54 := GetModelPricing("gpt-5.4"); gpt54.OutputPricePerMToken != 99.0 {
		t.Fatalf("gpt-5.4 override = %.2f, want 99", gpt54.OutputPricePerMToken)
	}

	// 清空覆盖 → 回退代码默认。
	SetModelPricingOverrides(nil)
	if p := GetModelPricing("gpt-5.4"); p.OutputPricePerMToken != 15.0 {
		t.Fatalf("after clear, output = %.2f, want 15", p.OutputPricePerMToken)
	}
}

func TestParseModelPricingOverridesJSON(t *testing.T) {
	m, err := ParseModelPricingOverridesJSON(`{"gpt-5.4":{"source":"custom","input":3,"output":20},"gpt-x":{}}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("empty override should be dropped, got %d entries: %v", len(m), m)
	}
	if m["gpt-5.4"].Input != 3 || m["gpt-5.4"].Output != 20 {
		t.Fatalf("parsed = %+v", m["gpt-5.4"])
	}
	if empty, _ := ParseModelPricingOverridesJSON(""); len(empty) != 0 {
		t.Fatalf("empty string should parse to empty map")
	}
}
