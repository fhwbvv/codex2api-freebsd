package auth

import "testing"

func TestIsPlusOrHigherPlan(t *testing.T) {
	tests := []struct {
		plan string
		want bool
	}{
		{"", false},
		{"free", false},
		{"Free", false},
		{"plus", true},
		{"pro", true},
		{"team", true},
		{"teamplus", true},
		{"enterprise", true},
		{"business", true},
		{"unknown", false},
	}

	for _, test := range tests {
		if got := IsPlusOrHigherPlan(test.plan); got != test.want {
			t.Fatalf("IsPlusOrHigherPlan(%q) = %v, want %v", test.plan, got, test.want)
		}
	}
}
