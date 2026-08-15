package proxy

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestStreamPhaseDiagnosticsDistinguishesBackpressure 验证断流判据能区分
// "下游背压拖停上游读取" 与 "上游自己重置"(issue #491)。
func TestStreamPhaseDiagnosticsDistinguishesBackpressure(t *testing.T) {
	// 无上游帧时不产出判据:连响应头都没读到,和背压无关。
	if got := newStreamPhaseDiagnostics().summary(); got != "" {
		t.Fatalf("no upstream frame should yield empty summary, got %q", got)
	}

	diag := newStreamPhaseDiagnostics()
	diag.markUpstreamFrame()
	diag.markUpstreamFrame()
	diag.addWriteBlocked(time.Now().Add(-1200 * time.Millisecond))
	summary := diag.summary()
	for _, want := range []string{"上游帧 2", "下游写阻塞 1", "距上游末帧"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}

	// nil 诊断器全程安全(未挂载时零开销路径)。
	var nilDiag *streamPhaseDiagnostics
	nilDiag.markUpstreamFrame()
	nilDiag.addWriteBlocked(time.Now())
	if got := nilDiag.summary(); got != "" {
		t.Fatalf("nil diagnostics summary = %q, want empty", got)
	}
	// 零值 start 不计入(未挂诊断器时 diagnosticsClock 返回零值)。
	zeroStart := newStreamPhaseDiagnostics()
	zeroStart.markUpstreamFrame()
	zeroStart.addWriteBlocked(time.Time{})
	if !strings.Contains(zeroStart.summary(), "下游写阻塞 0ms") {
		t.Fatalf("zero start must not accumulate: %s", zeroStart.summary())
	}
}

// TestAnnotateStreamBreakDiagnosticsOnlyForBreaks 验证判据只追加到断流失败上,
// 客户端主动断开与正常结束不受影响。
func TestAnnotateStreamBreakDiagnosticsOnlyForBreaks(t *testing.T) {
	diag := newStreamPhaseDiagnostics()
	diag.markUpstreamFrame()

	broke := classifyStreamOutcome(nil, errors.New("stream error: stream ID 19; INTERNAL_ERROR; received from peer"), nil, false)
	annotated := annotateStreamBreakDiagnostics(broke, diag)
	if !strings.Contains(annotated.failureMessage, "上游流读取失败") || !strings.Contains(annotated.failureMessage, "上游帧 1") {
		t.Fatalf("stream break message not annotated: %q", annotated.failureMessage)
	}
	if annotated.failureKind != "transport" || annotated.logStatusCode != logStatusUpstreamStreamBreak {
		t.Fatalf("annotation must not alter classification: kind=%q status=%d", annotated.failureKind, annotated.logStatusCode)
	}

	clientGone := classifyStreamOutcome(errors.New("context canceled"), nil, nil, false)
	if got := annotateStreamBreakDiagnostics(clientGone, diag); got.failureMessage != clientGone.failureMessage {
		t.Fatalf("client-close outcome must stay untouched: %q", got.failureMessage)
	}

	ok := classifyStreamOutcome(nil, nil, nil, true)
	if got := annotateStreamBreakDiagnostics(ok, diag); got.failureMessage != "" {
		t.Fatalf("successful stream must stay untouched: %q", got.failureMessage)
	}
}

// TestPoolEntryAgeRotation 验证按寿命轮转只作用于标准 transport,且 0 表示关闭。
func TestPoolEntryAgeRotation(t *testing.T) {
	now := time.Now().UnixNano()
	fresh := &poolEntry{createdAt: now - int64(5*time.Minute), rotatable: true}
	if fresh.expiredByAge(now, 30*time.Minute) {
		t.Fatal("young entry must not rotate")
	}
	old := &poolEntry{createdAt: now - int64(31*time.Minute), rotatable: true}
	if !old.expiredByAge(now, 30*time.Minute) {
		t.Fatal("entry past max age must rotate")
	}
	if old.expiredByAge(now, 0) {
		t.Fatal("max age 0 must disable rotation")
	}
	utls := &poolEntry{createdAt: now - int64(31*time.Minute), rotatable: false}
	if utls.expiredByAge(now, 30*time.Minute) {
		t.Fatal("uTLS entry must never rotate by age")
	}
	var nilEntry *poolEntry
	if nilEntry.expiredByAge(now, 30*time.Minute) {
		t.Fatal("nil entry must be safe")
	}
}

func TestDurationFromEnv(t *testing.T) {
	if got := durationFromEnv("CODEX_TEST_UNSET_DURATION", 15*time.Second); got != 15*time.Second {
		t.Fatalf("unset = %s, want 15s", got)
	}
	t.Setenv("CODEX_TEST_DURATION", "45s")
	if got := durationFromEnv("CODEX_TEST_DURATION", 15*time.Second); got != 45*time.Second {
		t.Fatalf("override = %s, want 45s", got)
	}
	t.Setenv("CODEX_TEST_DURATION", "0")
	if got := durationFromEnv("CODEX_TEST_DURATION", 15*time.Second); got != 0 {
		t.Fatalf("explicit zero = %s, want 0", got)
	}
	t.Setenv("CODEX_TEST_DURATION", "nonsense")
	if got := durationFromEnv("CODEX_TEST_DURATION", 15*time.Second); got != 15*time.Second {
		t.Fatalf("invalid must fall back, got %s", got)
	}
}
