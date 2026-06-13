package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeadersMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// Check security headers
	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}

	for header, expected := range headers {
		if value := w.Header().Get(header); value != expected {
			t.Errorf("Header %s = %q, expected %q", header, value, expected)
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data: blob: https: http:") {
		t.Errorf("Content-Security-Policy = %q, expected local, inline, and remote images to be allowed", csp)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "media-src 'self' data: blob: https: http:") {
		t.Errorf("Content-Security-Policy = %q, expected local, inline, and remote media to be allowed", csp)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self' https://api.github.com") {
		t.Errorf("Content-Security-Policy = %q, expected GitHub release API to be allowed for update checks", csp)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(2, time.Second))
	r.GET("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("Request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// Third request should be rate limited
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Errorf("Expected 429, got %d", w.Code)
	}
}

func TestRequestSizeLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestSizeLimiter(100))
	r.POST("/test", func(c *gin.Context) {
		body, _ := c.GetRawData()
		cached, exists := c.Get("raw_body")
		if !exists {
			t.Fatal("raw_body was not cached")
		}
		cachedBody, ok := cached.([]byte)
		if !ok {
			t.Fatalf("raw_body type = %T, want []byte", cached)
		}
		if string(cachedBody) != string(body) {
			t.Fatalf("cached body = %q, want %q", string(cachedBody), string(body))
		}
		c.String(200, "Received %d bytes", len(body))
	})

	// Small request should succeed
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", strings.NewReader("small"))
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Small request: expected 200, got %d", w.Code)
	}

	// Large request should fail with 413
	w = httptest.NewRecorder()
	largeBody := strings.Repeat("x", 200)
	req, _ = http.NewRequest("POST", "/test", strings.NewReader(largeBody))
	r.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Errorf("Large request: expected 413, got %d", w.Code)
	}
}

func TestRequestSizeLimiterAllowsEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestSizeLimiter(100))
	r.POST("/test", func(c *gin.Context) {
		body, _ := c.GetRawData()
		if len(body) != 0 {
			t.Fatalf("body length = %d, want 0", len(body))
		}
		cached, exists := c.Get("raw_body")
		if !exists {
			t.Fatal("raw_body was not cached for empty body")
		}
		cachedBody, ok := cached.([]byte)
		if !ok {
			t.Fatalf("raw_body type = %T, want []byte", cached)
		}
		if len(cachedBody) != 0 {
			t.Fatalf("cached body length = %d, want 0", len(cachedBody))
		}
		c.String(200, "OK")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", strings.NewReader(""))
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestIPRateLimiter(t *testing.T) {
	limiter := NewIPRateLimiter(3, time.Second)

	// First 3 requests should succeed
	for i := 0; i < 3; i++ {
		if !limiter.Allow("192.168.1.1") {
			t.Errorf("Request %d: expected allowed", i+1)
		}
	}

	// Fourth request should be blocked
	if limiter.Allow("192.168.1.1") {
		t.Error("Fourth request should be blocked")
	}

	// Different IP should be allowed
	if !limiter.Allow("192.168.1.2") {
		t.Error("Different IP should be allowed")
	}
}

func TestValidateContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidateContentType("application/json"))
	r.POST("/test", func(c *gin.Context) {
		c.String(200, "OK")
	})

	// Request with correct Content-Type should succeed
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("Correct Content-Type: expected 200, got %d", w.Code)
	}

	// Request with wrong Content-Type should fail
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/test", nil)
	req.Header.Set("Content-Type", "text/plain")
	r.ServeHTTP(w, req)
	if w.Code != 415 {
		t.Errorf("Wrong Content-Type: expected 415, got %d", w.Code)
	}
}

func TestIsSensitiveEndpoint(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/api/admin/accounts", true},
		{"/api/admin/keys", true},
		{"/api/admin/settings", true},
		{"/api/admin/proxies", true},
		{"/api/admin/stats", false},
		{"/health", false},
		{"/v1/chat/completions", false},
	}

	for _, test := range tests {
		result := IsSensitiveEndpoint(test.path)
		if result != test.expected {
			t.Errorf("IsSensitiveEndpoint(%q) = %v, expected %v", test.path, result, test.expected)
		}
	}
}

func TestGenerateSecureToken(t *testing.T) {
	// Test default length
	token, err := GenerateSecureToken(0)
	if err != nil {
		t.Errorf("GenerateSecureToken(0) error: %v", err)
	}
	if len(token) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("GenerateSecureToken(0) length = %d, expected 64", len(token))
	}

	// Test custom length
	token, err = GenerateSecureToken(16)
	if err != nil {
		t.Errorf("GenerateSecureToken(16) error: %v", err)
	}
	if len(token) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("GenerateSecureToken(16) length = %d, expected 32", len(token))
	}
}
