package main

import (
	"testing"
	"time"
)

func TestHealthLockProbeAccumulatesOnlyConsecutiveFailures(t *testing.T) {
	probe := &healthLockProbe{}
	base := time.Unix(1_700_000_000, 0)

	if got := probe.observe(false, base); got != 0 {
		t.Fatalf("first failure duration = %s, want 0", got)
	}
	if got := probe.observe(false, base.Add(10*time.Second)); got != 10*time.Second {
		t.Fatalf("second failure duration = %s, want 10s", got)
	}
	if got := probe.observe(false, base.Add(31*time.Second)); got < healthStoreLockStallThreshold {
		t.Fatalf("stalled duration = %s, want >= %s", got, healthStoreLockStallThreshold)
	}
}

func TestHealthLockProbeResetsOnSuccess(t *testing.T) {
	probe := &healthLockProbe{}
	base := time.Unix(1_700_000_000, 0)

	probe.observe(false, base)
	if got := probe.observe(true, base.Add(20*time.Second)); got != 0 {
		t.Fatalf("success duration = %s, want 0", got)
	}
	// 成功之后重新失败要从零开始累计,而不是接着上一段算。
	if got := probe.observe(false, base.Add(40*time.Second)); got != 0 {
		t.Fatalf("restarted failure duration = %s, want 0", got)
	}
	if got := probe.observe(false, base.Add(50*time.Second)); got != 10*time.Second {
		t.Fatalf("restarted second failure duration = %s, want 10s", got)
	}
}
