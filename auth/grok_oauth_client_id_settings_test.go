package auth

import "testing"

// TestGrokOAuthClientIDPrecedence 钉住三级优先级：环境变量 > 系统设置 > 内置默认。
// 环境变量压在系统设置之上是刻意的：数据库里的值被误改时还能从部署侧兜回来。
func TestGrokOAuthClientIDPrecedence(t *testing.T) {
	t.Run("都没配时用内置默认", func(t *testing.T) {
		t.Setenv(EnvGrokOAuthClientID, "")
		SetConfiguredGrokOAuthClientID("")
		t.Cleanup(func() { SetConfiguredGrokOAuthClientID("") })
		if got := EffectiveGrokOAuthClientID(); got != GrokDefaultOAuthClientID {
			t.Fatalf("EffectiveGrokOAuthClientID() = %q, want %q", got, GrokDefaultOAuthClientID)
		}
	})

	t.Run("只配系统设置时用系统设置", func(t *testing.T) {
		t.Setenv(EnvGrokOAuthClientID, "")
		SetConfiguredGrokOAuthClientID("from-settings")
		t.Cleanup(func() { SetConfiguredGrokOAuthClientID("") })
		if got := EffectiveGrokOAuthClientID(); got != "from-settings" {
			t.Fatalf("EffectiveGrokOAuthClientID() = %q, want from-settings", got)
		}
	})

	t.Run("环境变量压过系统设置", func(t *testing.T) {
		t.Setenv(EnvGrokOAuthClientID, "from-env")
		SetConfiguredGrokOAuthClientID("from-settings")
		t.Cleanup(func() { SetConfiguredGrokOAuthClientID("") })
		if got := EffectiveGrokOAuthClientID(); got != "from-env" {
			t.Fatalf("EffectiveGrokOAuthClientID() = %q, want from-env", got)
		}
	})

	t.Run("环境变量纯空白不算配置", func(t *testing.T) {
		t.Setenv(EnvGrokOAuthClientID, "   ")
		SetConfiguredGrokOAuthClientID("from-settings")
		t.Cleanup(func() { SetConfiguredGrokOAuthClientID("") })
		if got := EffectiveGrokOAuthClientID(); got != "from-settings" {
			t.Fatalf("EffectiveGrokOAuthClientID() = %q, want from-settings", got)
		}
	})
}

// TestNormalizeGrokOAuthClientID client_id 会拼进授权 URL 与 token 表单，
// 带空白/控制字符或超长的一律视为未配置。
func TestNormalizeGrokOAuthClientID(t *testing.T) {
	long := make([]byte, GrokOAuthClientIDMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"正常 id 原样保留", "b1a00492-073a-47ea-816f-4c329264a828", "b1a00492-073a-47ea-816f-4c329264a828"},
		{"去首尾空白", "  client-id  ", "client-id"},
		{"空串", "", ""},
		{"纯空白", " \t ", ""},
		{"中间含空格视为非法", "client id", ""},
		{"含换行视为非法", "client\nid", ""},
		{"含制表符视为非法", "client\tid", ""},
		{"含控制字符视为非法", "client\x00id", ""},
		{"超长视为非法", string(long), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeGrokOAuthClientID(tc.in); got != tc.want {
				t.Fatalf("NormalizeGrokOAuthClientID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestGrokOAuthClientIDFromConfig 系统设置的 grok_config JSON 解析。
func TestGrokOAuthClientIDFromConfig(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"空配置", "", ""},
		{"没有该字段", `{"affinity_mode":"strict"}`, ""},
		{"正常取值", `{"oauth_client_id":"custom-id"}`, "custom-id"},
		{"非法 JSON", `{oops`, ""},
		{"值含空白视为未配置", `{"oauth_client_id":"a b"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grokOAuthClientIDFromConfig(tc.raw); got != tc.want {
				t.Fatalf("grokOAuthClientIDFromConfig(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
