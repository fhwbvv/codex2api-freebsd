package admin

import (
	"net/http"
	"testing"
)

func TestShouldMarkBatchTestAccountError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       []byte
		want       bool
	}{
		{
			name:       "forbidden is account scoped",
			statusCode: http.StatusForbidden,
			body:       []byte(`{"error":{"code":"unsupported_country_region_territory"}}`),
			want:       true,
		},
		{
			name:       "invalid grant bad request is account scoped",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"error":"invalid_grant"}`),
			want:       true,
		},
		{
			name:       "model version bad request is global",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"detail":"The 'gpt-5.5' model requires a newer version of Codex"}`),
			want:       false,
		},
		{
			name:       "server error is not marked as account error",
			statusCode: http.StatusBadGateway,
			body:       []byte(`bad gateway`),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldMarkBatchTestAccountError(tt.statusCode, tt.body); got != tt.want {
				t.Fatalf("shouldMarkBatchTestAccountError() = %v, want %v", got, tt.want)
			}
		})
	}
}
