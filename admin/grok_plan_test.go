package admin

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestGrokPlanTypeFromCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		credentials map[string]interface{}
		want        string
	}{
		{
			name:        "missing tier stays blank",
			credentials: map[string]interface{}{},
			want:        "",
		},
		{
			name:        "api key remains an auth marker",
			credentials: map[string]interface{}{"api_key": "xai-test"},
			want:        "api",
		},
		{
			name:        "legacy display is canonicalized",
			credentials: map[string]interface{}{"plan_type": "SuperGrok Heavy"},
			want:        "supergrok_heavy",
		},
		{
			name: "access token tier wins over stored value",
			credentials: map[string]interface{}{
				"access_token": testGrokJWT(map[string]interface{}{"tier": 4}),
				"plan_type":    "free",
			},
			want: "x_premium_plus",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := grokPlanTypeFromCredentials(tt.credentials); got != tt.want {
				t.Fatalf("grokPlanTypeFromCredentials() = %q, want %q", got, tt.want)
			}
		})
	}
}

func testGrokJWT(claims map[string]interface{}) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}
