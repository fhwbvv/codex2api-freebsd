package auth

import (
	"testing"
	"time"
)

// 脏位语义:观测更新置脏,Take 取走后清脏;启动恢复(markDirty=false)不触发落库。
func TestGrokRateLimitSnapshotDirtyLifecycle(t *testing.T) {
	acc := &Account{UpstreamType: UpstreamGrok}

	if _, dirty := acc.TakeGrokRateLimitSnapshotIfDirty(); dirty {
		t.Fatal("empty account should not be dirty")
	}

	now := time.Now()
	acc.SetGrokRateLimitSnapshot(GrokRateLimitSnapshot{LimitTokens: 100, RemainingTokens: 60, UpdatedAt: now})
	snap, dirty := acc.TakeGrokRateLimitSnapshotIfDirty()
	if !dirty || snap.RemainingTokens != 60 {
		t.Fatalf("take after set = (%+v, %v), want dirty with remaining 60", snap, dirty)
	}
	if _, dirty := acc.TakeGrokRateLimitSnapshotIfDirty(); dirty {
		t.Fatal("second take should be clean")
	}

	// 时间倒流的旧观测被忽略,也不置脏。
	acc.SetGrokRateLimitSnapshot(GrokRateLimitSnapshot{LimitTokens: 100, RemainingTokens: 99, UpdatedAt: now.Add(-time.Minute)})
	if _, dirty := acc.TakeGrokRateLimitSnapshotIfDirty(); dirty {
		t.Fatal("stale observation should not mark dirty")
	}

	// 启动恢复路径:写入内存但不置脏(值本来自库里)。
	acc2 := &Account{UpstreamType: UpstreamGrok}
	acc2.setGrokRateLimitSnapshot(GrokRateLimitSnapshot{LimitTokens: 100, RemainingTokens: 10, UpdatedAt: now}, false)
	if got, ok := acc2.GetGrokRateLimitSnapshot(); !ok || got.RemainingTokens != 10 {
		t.Fatalf("restored snapshot = (%+v, %v), want remaining 10", got, ok)
	}
	if _, dirty := acc2.TakeGrokRateLimitSnapshotIfDirty(); dirty {
		t.Fatal("restore must not mark dirty")
	}
}
