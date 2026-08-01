package auth

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// GrokPlan describes one Grok subscription tier. Key is the stable value stored
// in plan_type; Display is the user-facing label. Paid and Billing mirror the
// entitlement flags encoded by xAI's access-token tier claim.
type GrokPlan struct {
	Key     string `json:"key"`
	Display string `json:"display"`
	Paid    bool   `json:"paid"`
	Billing bool   `json:"billing"`
}

var grokPlansByTier = map[string]GrokPlan{
	"0": {Key: "free", Display: "Free"},
	"1": {Key: "supergrok", Display: "SuperGrok", Paid: true, Billing: true},
	"2": {Key: "x_basic", Display: "X Basic", Paid: true},
	"3": {Key: "x_premium", Display: "X Premium", Paid: true, Billing: true},
	"4": {Key: "x_premium_plus", Display: "X Premium+", Paid: true, Billing: true},
	"5": {Key: "supergrok_heavy", Display: "SuperGrok Heavy", Paid: true, Billing: true},
	"6": {Key: "supergrok_lite", Display: "SuperGrok Lite", Paid: true, Billing: true},
}

var grokPlanAliases = map[string]string{
	"free":            "0",
	"supergrok":       "1",
	"x_basic":         "2",
	"xbasic":          "2",
	"x_premium":       "3",
	"xpremium":        "3",
	"x_premium_plus":  "4",
	"xpremium_plus":   "4",
	"xpremiumplus":    "4",
	"supergrok_heavy": "5",
	"supergrokheavy":  "5",
	"supergrok_lite":  "6",
	"supergroklite":   "6",
}

// GrokPlanFromTier maps the numeric tier claim carried by Grok OAuth access
// tokens. Unknown positive values remain visible as their numeric string so a
// newly introduced upstream tier is not silently mislabeled. Negative, missing,
// or non-numeric values are invalid.
func GrokPlanFromTier(value any) (GrokPlan, bool) {
	tier, ok := grokTierNumber(value)
	if !ok || tier < 0 {
		return GrokPlan{}, false
	}
	key := strconv.FormatFloat(tier, 'f', -1, 64)
	if plan, exists := grokPlansByTier[key]; exists {
		return plan, true
	}
	if tier > 0 {
		return GrokPlan{
			Key:     key,
			Display: key,
			Paid:    true,
			Billing: true,
		}, true
	}
	return GrokPlan{}, false
}

// ResolveGrokPlan accepts either a tier number, a canonical key, or a known
// display label. This keeps existing rows such as "SuperGrok Heavy" compatible
// while new writes use the stable snake_case key.
func ResolveGrokPlan(value any) (GrokPlan, bool) {
	if raw, ok := value.(string); ok {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return GrokPlan{}, false
		}
		if tier, exists := grokPlanAliases[normalizeGrokPlanAlias(raw)]; exists {
			return grokPlansByTier[tier], true
		}
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			return GrokPlanFromTier(parsed)
		}
		return GrokPlan{}, false
	}
	return GrokPlanFromTier(value)
}

// GrokPlanFromAccessToken reads the unverified JWT payload already used for
// Grok identity and expiry extraction and resolves its numeric tier claim.
func GrokPlanFromAccessToken(accessToken string) (GrokPlan, bool) {
	claims := grokJWTClaims(accessToken)
	if claims == nil {
		return GrokPlan{}, false
	}
	return GrokPlanFromTier(claims["tier"])
}

// GrokPlanTypeFromAccessToken returns the stable plan_type key or an empty
// string when the token does not contain a valid tier claim.
func GrokPlanTypeFromAccessToken(accessToken string) string {
	plan, ok := GrokPlanFromAccessToken(accessToken)
	if !ok {
		return ""
	}
	return plan.Key
}

func normalizeGrokPlanAlias(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "+", "_plus")
	normalized = strings.NewReplacer(" ", "_", "-", "_").Replace(normalized)
	for strings.Contains(normalized, "__") {
		normalized = strings.ReplaceAll(normalized, "__", "_")
	}
	return strings.Trim(normalized, "_")
}

func grokTierNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}
