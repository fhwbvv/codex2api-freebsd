package proxy

import (
	"testing"
	"time"

	"github.com/codex2api/database"
)

// issue #446：uTLS 连接优雅关闭的等待上限从硬编码常量改为系统设置项。
// 以下测试锁死默认值、夹取边界与 DB→运行时的下发链路。

func TestUTLSShutdownTimeoutDefaultIs30Min(t *testing.T) {
	if got := DefaultRuntimeSettings().UTLSShutdownTimeout(); got != 30*time.Minute {
		t.Fatalf("默认 uTLS 优雅关闭上限 = %s, want 30m", got)
	}
	// 未配置（零值）时必须回落默认，而不是变成 0 —— 0 会让 context 立即超时，
	// 把在途的流式回答直接截断。
	var zero RuntimeSettings
	if got := zero.UTLSShutdownTimeout(); got != 30*time.Minute {
		t.Fatalf("零值 uTLS 优雅关闭上限 = %s, want 30m（0 会立即截断在途请求）", got)
	}
}

func TestNormalizeUTLSShutdownTimeoutMinutesClamp(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 30},     // 未配置回落默认
		{-5, 30},    // 非法负值回落默认
		{1, 1},      // 下界
		{30, 30},    // 默认值原样
		{240, 240},  // 上界
		{1000, 240}, // 越界夹到上界
	}
	for _, c := range cases {
		if got := database.NormalizeUTLSShutdownTimeoutMinutes(c.in); got != c.want {
			t.Errorf("NormalizeUTLSShutdownTimeoutMinutes(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// 运行时归一化必须把越界值夹住，避免管理端绕过校验写入极端值。
func TestNormalizeRuntimeSettingsClampsUTLSShutdownTimeout(t *testing.T) {
	s := DefaultRuntimeSettings()
	s.UTLSShutdownTimeoutMin = 99999
	if got := NormalizeRuntimeSettings(s).UTLSShutdownTimeoutMin; got != 240 {
		t.Fatalf("越界值归一 = %d, want 240", got)
	}
	s.UTLSShutdownTimeoutMin = 0
	if got := NormalizeRuntimeSettings(s).UTLSShutdownTimeoutMin; got != 30 {
		t.Fatalf("零值归一 = %d, want 30", got)
	}
}

// DB 设置必须真正下发到运行时，并被 transport 读到。
func TestApplyRuntimeSettingsFromSystemUTLSShutdownTimeout(t *testing.T) {
	original := CurrentRuntimeSettings()
	t.Cleanup(func() { ApplyRuntimeSettings(original) })

	next := ApplyRuntimeSettingsFromSystem(&database.SystemSettings{
		UTLSShutdownTimeoutMinutes: 5,
	})
	if next.UTLSShutdownTimeoutMin != 5 {
		t.Fatalf("下发后 UTLSShutdownTimeoutMin = %d, want 5", next.UTLSShutdownTimeoutMin)
	}
	// transport 侧的取值函数必须跟随设置变化（此前是编译期常量）。
	if got := utlsShutdownTimeout(); got != 5*time.Minute {
		t.Fatalf("transport 读到的上限 = %s, want 5m", got)
	}

	// 未配置该字段的旧库/旧实例快照要回落默认，不能变成 0。
	def := ApplyRuntimeSettingsFromSystem(&database.SystemSettings{})
	if def.UTLSShutdownTimeoutMin != 30 {
		t.Fatalf("缺字段快照回落 = %d, want 30", def.UTLSShutdownTimeoutMin)
	}
	if got := utlsShutdownTimeout(); got != 30*time.Minute {
		t.Fatalf("回落后 transport 读到 = %s, want 30m", got)
	}
}
