package proxy

import "testing"

func TestIsMissingScopeUnauthorized(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "missing scope for responses write",
			body: `{"error":{"message":"Missing required scope: api.responses.write","type":"invalid_request_error","code":"missing_scope"}}`,
			want: true,
		},
		{
			name: "missing scope generic message",
			body: `{"error":{"message":"missing scope for this operation","type":"invalid_request_error","code":"missing_scope"}}`,
			want: true,
		},
		{
			name: "unauthorized invalid api key",
			body: `{"error":{"message":"Invalid API key","type":"invalid_request_error","code":"invalid_api_key"}}`,
			want: false,
		},
		{
			name: "empty body",
			body: ``,
			want: false,
		},
		{
			name: "invalid json",
			body: `not-json`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMissingScopeUnauthorized([]byte(tt.body))
			if got != tt.want {
				t.Fatalf("isMissingScopeUnauthorized() = %v, want %v", got, tt.want)
			}
		})
	}
}
