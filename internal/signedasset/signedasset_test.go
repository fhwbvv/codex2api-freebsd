package signedasset

import (
	"bytes"
	"crypto/sha256"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConfiguredImageAssetSecretIsDeterministic(t *testing.T) {
	t.Setenv(imageAssetSigningSecretEnv, "persistent-image-asset-signing-secret")
	secretOnce = sync.Once{}
	secret = nil
	t.Cleanup(func() {
		secretOnce = sync.Once{}
		secret = nil
	})

	want := sha256.Sum256([]byte("persistent-image-asset-signing-secret"))
	if got := imageAssetSecret(); !bytes.Equal(got, want[:]) {
		t.Fatal("configured secret digest mismatch")
	}
}

func TestImageAssetURLUsesConfiguredPublicBaseURL(t *testing.T) {
	t.Setenv(imageAssetPublicBaseURLEnv, "https://cdn.example.com/")
	raw := ImageAssetURLWithTTL(42, 0, time.Hour)
	if !strings.HasPrefix(raw, "https://cdn.example.com/p/img/42?") {
		t.Fatalf("unexpected public URL: %s", raw)
	}
}

func TestImageAssetURLIgnoresInvalidPublicBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"javascript:alert(1)",
		"https://cdn.example.com?tenant=images",
		"https://cdn.example.com?",
		"https://cdn.example.com#fragment",
		"https://user:password@cdn.example.com",
	} {
		t.Run(baseURL, func(t *testing.T) {
			t.Setenv(imageAssetPublicBaseURLEnv, baseURL)
			raw := ImageAssetURLWithTTL(42, 0, time.Hour)
			if !strings.HasPrefix(raw, "/p/img/42?") {
				t.Fatalf("unexpected fallback URL: %s", raw)
			}
		})
	}
}

func TestImageAssetURLVerifies(t *testing.T) {
	raw := ImageAssetURLWithTTL(42, 32, time.Hour)
	if !strings.HasPrefix(raw, "/p/img/42?") {
		t.Fatalf("unexpected URL: %s", raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	values := parsed.Query()
	exp, err := strconv.ParseInt(values.Get("exp"), 10, 64)
	if err != nil {
		t.Fatalf("parse exp: %v", err)
	}
	if !VerifyImageAssetURL(42, exp, 32, values.Get("sig"), time.Now()) {
		t.Fatal("expected signed URL to verify")
	}
}

func TestVerifyImageAssetURLRejectsTampering(t *testing.T) {
	raw := ImageAssetURLWithTTL(42, 0, time.Hour)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	values := parsed.Query()
	exp, err := strconv.ParseInt(values.Get("exp"), 10, 64)
	if err != nil {
		t.Fatalf("parse exp: %v", err)
	}
	if VerifyImageAssetURL(43, exp, 0, values.Get("sig"), time.Now()) {
		t.Fatal("expected asset id tampering to fail")
	}
	if VerifyImageAssetURL(42, exp, 32, values.Get("sig"), time.Now()) {
		t.Fatal("expected thumb tampering to fail")
	}
	if VerifyImageAssetURL(42, exp, 0, values.Get("sig"), time.Unix(exp+1, 0)) {
		t.Fatal("expected expired URL to fail")
	}
}
