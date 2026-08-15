package admin

import (
	"reflect"
	"testing"
)

func TestCleanPlanAllowAcceptsGrokLiveTiersAndAPI(t *testing.T) {
	input := []string{
		"api", "SuperGrok", "x_basic", "x_premium", "x_premium_plus",
		"supergrok_heavy", "supergrok_lite", "supergrok_plus", "unknown", "SUPERGROK",
	}
	want := []string{
		"api", "supergrok", "x_basic", "x_premium", "x_premium_plus",
		"supergrok_heavy", "supergrok_lite", "supergrok_plus",
	}
	if got := cleanPlanAllow(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanPlanAllow() = %#v, want %#v", got, want)
	}
}
