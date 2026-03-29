package security

import (
	"strings"
	"testing"
)

func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello world"},
		{"hello\x00world", "helloworld"},
		{"hello\x01\x02\x03world", "helloworld"},
		{"hello\nworld", "hello\nworld"},
		{"hello\tworld", "hello\tworld"},
		{"", ""},
	}

	for _, test := range tests {
		result := SanitizeInput(test.input)
		if result != test.expected {
			t.Errorf("SanitizeInput(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestContainsXSS(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"<script>alert(1)</script>", true},
		{"javascript:alert(1)", true},
		{"onclick=\"alert(1)\"", true},
		{"<iframe src=evil.com>", true},
		{"hello world", false},
		{"gpt-5.4", false},
		{"model-name", false},
	}

	for _, test := range tests {
		result := ContainsXSS(test.input)
		if result != test.expected {
			t.Errorf("ContainsXSS(%q) = %v, expected %v", test.input, result, test.expected)
		}
	}
}

func TestContainsSQLInjection(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"SELECT * FROM users", true},
		{"DELETE FROM accounts", true},
		{"1 OR 1=1", true},
		{"1; DROP TABLE users", true},
		{"hello world", false},
		{"gpt-5.4", false},
		{"model-name_123", false},
	}

	for _, test := range tests {
		result := ContainsSQLInjection(test.input)
		if result != test.expected {
			t.Errorf("ContainsSQLInjection(%q) = %v, expected %v", test.input, result, test.expected)
		}
	}
}

func TestMaskSensitiveData(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"refresh_token=secret123", "refresh_token=****MASKED****"},
		{"access_token: abc123", "access_token: ****MASKED****"},
		{"Bearer token123", "Bearer ****MASKED****"},
		{"api_key=sk-1234567890", "api_key=****MASKED****"},
		{"password: mypassword", "password: ****MASKED****"},
		{"sk-abcdef1234567890abcdef", "sk-****MASKED****"},
		{"hello world", "hello world"},
		{"token=550e8400-e29b-41d4-a716-446655440000", "token=****UUID-MASKED****"},
	}

	for _, test := range tests {
		result := MaskSensitiveData(test.input)
		if result != test.expected {
			t.Errorf("MaskSensitiveData(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sk-abcdefghijklmnopqrstuvwxyz", "sk-a****...****wxyz"},
		{"sk-short", "****"},
		{"", "****"},
		{"sk-" + strings.Repeat("a", 100), "sk-a****...****aaaa"},
	}

	for _, test := range tests {
		result := MaskAPIKey(test.input)
		if result != test.expected {
			t.Errorf("MaskAPIKey(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user@example.com", "us****@example.com"},
		{"test@gmail.com", "te****@gmail.com"},
		{"a@b.com", "****"},
		{"", ""},
		{"invalid-email", "****"},
	}

	for _, test := range tests {
		result := MaskEmail(test.input)
		if result != test.expected {
			t.Errorf("MaskEmail(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestValidateModelName(t *testing.T) {
	tests := []struct {
		input   string
		isError bool
	}{
		{"gpt-5.4", false},
		{"gpt-5-codex", false},
		{"", false}, // empty is allowed
		{strings.Repeat("a", 101), true}, // too long
		{"model<script>", true},          // invalid chars
	}

	for _, test := range tests {
		err := ValidateModelName(test.input)
		if test.isError && err == nil {
			t.Errorf("ValidateModelName(%q) expected error", test.input)
		}
		if !test.isError && err != nil {
			t.Errorf("ValidateModelName(%q) unexpected error: %v", test.input, err)
		}
	}
}

func TestSecureCompare(t *testing.T) {
	tests := []struct {
		a        string
		b        string
		expected bool
	}{
		{"secret", "secret", true},
		{"secret", "different", false},
		{"secret", "Secret", false},
		{"", "", true},
		{"", "secret", false},
		{"secret", "", false},
	}

	for _, test := range tests {
		result := SecureCompare(test.a, test.b)
		if result != test.expected {
			t.Errorf("SecureCompare(%q, %q) = %v, expected %v", test.a, test.b, result, test.expected)
		}
	}
}

func TestSafeTruncate(t *testing.T) {
	tests := []struct {
		input   string
		maxLen  int
		expected string
	}{
		{"hello world", 5, "hello"},
		{"hello", 10, "hello"},
		{"", 5, ""},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"世界hello", 3, "世界h"},
	}

	for _, test := range tests {
		result := SafeTruncate(test.input, test.maxLen)
		if result != test.expected {
			t.Errorf("SafeTruncate(%q, %d) = %q, expected %q", test.input, test.maxLen, result, test.expected)
		}
	}
}
