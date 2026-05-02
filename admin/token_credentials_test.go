package admin

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func makeAdminTestJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".fake_signature"
}

func TestNormalizeTokenCredentialSeedPrefersAccessTokenExpiry(t *testing.T) {
	accessExpiresAt := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	rawExpired := time.Now().Add(-time.Hour).Format(time.RFC3339)
	accessToken := makeAdminTestJWT(t, map[string]interface{}{
		"exp": accessExpiresAt.Unix(),
	})

	seed := normalizeTokenCredentialSeed(tokenCredentialSeed{
		accessToken:  accessToken,
		expiresAtRaw: rawExpired,
	})

	if !seed.expiresAt.Equal(accessExpiresAt) {
		t.Fatalf("expiresAt = %s, want access token expiry %s", seed.expiresAt, accessExpiresAt)
	}
}
