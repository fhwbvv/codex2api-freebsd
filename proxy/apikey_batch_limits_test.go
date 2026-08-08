package proxy

import "testing"

func TestAPIKeyBatchWouldExceed(t *testing.T) {
	tests := []struct {
		name     string
		current  int64
		limit    int
		requests int
		want     bool
	}{
		{name: "fits exactly", current: 2, limit: 4, requests: 2, want: false},
		{name: "crosses limit", current: 2, limit: 4, requests: 3, want: true},
		{name: "single over remaining", current: 4, limit: 4, requests: 1, want: true},
		{name: "unlimited", current: 100, limit: 0, requests: 4, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := apiKeyBatchWouldExceed(test.current, test.limit, test.requests); got != test.want {
				t.Fatalf("apiKeyBatchWouldExceed(%d, %d, %d) = %t, want %t", test.current, test.limit, test.requests, got, test.want)
			}
		})
	}
}
