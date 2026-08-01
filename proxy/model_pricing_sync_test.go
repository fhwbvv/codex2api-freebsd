package proxy

import (
	"testing"
)

func TestParseModelPricingPayloadFlat(t *testing.T) {
	body := []byte(`{
		"gpt-5.5": {"input": 5, "cached_input": 0.5, "output": 30, "input_priority": 10},
		"empty-model": {}
	}`)
	got, err := parseModelPricingPayload(body)
	if err != nil {
		t.Fatalf("parse flat payload: %v", err)
	}
	ov, ok := got["gpt-5.5"]
	if !ok {
		t.Fatalf("missing gpt-5.5, got keys: %v", got)
	}
	if ov.Input != 5 || ov.CachedInput != 0.5 || ov.Output != 30 || ov.InputPriority != 10 {
		t.Fatalf("unexpected override: %+v", ov)
	}
}

func TestParseModelPricingPayloadModelsDev(t *testing.T) {
	body := []byte(`{
		"openai": {
			"id": "openai",
			"models": {
				"gpt-5.5": {
					"id": "gpt-5.5",
					"cost": {
						"input": 5, "output": 30, "cache_read": 0.5,
						"tiers": [{"input": 10, "output": 45, "cache_read": 1, "tier": {"type": "context", "size": 272000}}]
					}
				},
				"gpt-5.4": {"id": "gpt-5.4", "cost": {"input": 2.5, "output": 15, "cache_read": 0.25}},
				"gpt-5.6-terra": {"id": "gpt-5.6-terra", "cost": {"input": 99, "output": 99, "cache_read": 99}},
				"text-embedding-3-large": {"id": "text-embedding-3-large"}
			}
		},
		"anthropic": {
			"id": "anthropic",
			"models": {"gpt-5.5": {"cost": {"input": 1, "output": 1}}}
		}
	}`)
	got, err := parseModelPricingPayload(body)
	if err != nil {
		t.Fatalf("parse models.dev payload: %v", err)
	}

	ov, ok := got["gpt-5.5"]
	if !ok {
		t.Fatalf("missing gpt-5.5, got keys: %v", got)
	}
	// 只取 openai provider，长上下文分档映射到 *_long。
	if ov.Input != 5 || ov.CachedInput != 0.5 || ov.Output != 30 {
		t.Fatalf("unexpected standard tier: %+v", ov)
	}
	if ov.InputLong != 10 || ov.CachedInputLong != 1 || ov.OutputLong != 45 {
		t.Fatalf("unexpected long-context tier: %+v", ov)
	}

	// gpt-5.6-terra 独立规范键，与 gpt-5.4 互不影响。
	if ov44 := got["gpt-5.4"]; ov44.Input != 2.5 {
		t.Fatalf("gpt-5.4 should keep exact entry: %+v", ov44)
	}
	if ovTerra, ok := got["gpt-5.6-terra"]; !ok {
		t.Fatalf("missing gpt-5.6-terra as independent pricing key")
	} else if ovTerra.Input != 99 {
		t.Fatalf("gpt-5.6-terra = %+v, want input 99", ovTerra)
	}

	// 无 cost 的模型跳过。
	if _, ok := got["text-embedding-3-large"]; ok {
		t.Fatalf("model without cost should be skipped")
	}
}

// models.dev 同步要覆盖 xai provider：网关同时代理 Grok，grok-4.5 等必须拿到官方价，
// 转售 provider（同名模型带加价）仍然不参与。
func TestParseModelPricingPayloadModelsDevIncludesXAI(t *testing.T) {
	body := []byte(`{
		"openai": {
			"id": "openai",
			"models": {"gpt-5.5": {"id": "gpt-5.5", "cost": {"input": 5, "output": 30, "cache_read": 0.5}}}
		},
		"xai": {
			"id": "xai",
			"models": {
				"grok-4.5": {
					"id": "grok-4.5",
					"cost": {
						"input": 2, "output": 6, "cache_read": 0.3,
						"tiers": [{"input": 4, "output": 12, "cache_read": 1, "tier": {"type": "context", "size": 200000}}]
					}
				}
			}
		},
		"some-reseller": {
			"id": "some-reseller",
			"models": {"grok-4.5": {"cost": {"input": 99, "output": 99}}}
		}
	}`)

	got, err := parseModelPricingPayload(body)
	if err != nil {
		t.Fatalf("parse models.dev payload: %v", err)
	}

	ov, ok := got["grok-4.5"]
	if !ok {
		t.Fatalf("missing grok-4.5, got keys: %v", got)
	}
	if ov.Input != 2 || ov.Output != 6 || ov.CachedInput != 0.3 {
		t.Fatalf("grok-4.5 standard tier = %+v, want xai rates (not the reseller's)", ov)
	}
	if ov.InputLong != 4 || ov.OutputLong != 12 || ov.CachedInputLong != 1 {
		t.Fatalf("grok-4.5 long tier = %+v", ov)
	}
	if ov5 := got["gpt-5.5"]; ov5.Input != 5 {
		t.Fatalf("openai entries must survive alongside xai: %+v", ov5)
	}
}
