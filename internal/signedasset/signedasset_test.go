package signedasset

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
