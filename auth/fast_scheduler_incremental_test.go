package auth

import (
	"testing"
)

// TestFastSchedulerBulkInsertDoesNotResortPerEntry 锁定"写入侧不再逐条整桶重排"。
// 修复前每次 Update/insert 都会整桶排序 + 全量重建位置索引，号池上万时会把调度器
// 独占锁占满，批量导入期间整个网关排队（表现为导入时服务卡死）。
func TestFastSchedulerBulkInsertDoesNotResortPerEntry(t *testing.T) {
	const seed = 300
	const added = 200

	scheduler := NewFastScheduler(2, "round_robin")
	accounts := make([]*Account, 0, seed)
	for i := 1; i <= seed; i++ {
		accounts = append(accounts, newFastSchedulerTestAccount(int64(i), HealthTierHealthy, float64(i%37), 2))
	}
	scheduler.Rebuild(accounts)

	before := scheduler.ResortCount()
	for i := 1; i <= added; i++ {
		scheduler.Update(newFastSchedulerTestAccount(int64(seed+i), HealthTierHealthy, float64(i%37), 2))
	}
	writeResorts := scheduler.ResortCount() - before
	if writeResorts != 0 {
		t.Fatalf("批量写入触发了 %d 次整桶重排，期望 0（重排应推迟到取号前合并）", writeResorts)
	}

	// 取号时才补排，且一批写入只合并成一次。
	if acc := scheduler.Acquire(); acc == nil {
		t.Fatal("Acquire() 返回 nil，期望取到账号")
	} else {
		scheduler.Release(acc)
	}
	if got := scheduler.ResortCount() - before; got != 1 {
		t.Fatalf("取号后累计重排 %d 次，期望恰好 1 次", got)
	}
}

// TestFastSchedulerIncrementalOrderMatchesRebuild 保证惰性重排后的桶顺序与
// 全量 Rebuild 完全一致——增量路径不能改变调度语义。
func TestFastSchedulerIncrementalOrderMatchesRebuild(t *testing.T) {
	for _, mode := range []string{"round_robin", "remaining_quota", "fill_first", "dispatch_score"} {
		t.Run(mode, func(t *testing.T) {
			const n = 60

			build := func() []*Account {
				accounts := make([]*Account, 0, n)
				for i := 1; i <= n; i++ {
					acc := newFastSchedulerTestAccount(int64(i), HealthTierHealthy, float64((i*7)%23), 2)
					acc.UsagePercent7d = float64((i * 13) % 41)
					acc.UsagePercent7dValid = true
					acc.SchedulerPriority = int64(i % 3)
					accounts = append(accounts, acc)
				}
				return accounts
			}

			// 参照组：一次性 Rebuild。
			reference := NewFastScheduler(2, mode)
			reference.Rebuild(build())

			// 对照组：逐条 Update 后惰性重排。
			incremental := NewFastScheduler(2, mode)
			for _, acc := range build() {
				incremental.Update(acc)
			}
			incremental.mu.Lock()
			incremental.ensureSortedLocked()
			incremental.mu.Unlock()

			reference.mu.RLock()
			want := append([]fastSchedulerEntry(nil), reference.buckets[HealthTierHealthy]...)
			reference.mu.RUnlock()
			incremental.mu.RLock()
			got := append([]fastSchedulerEntry(nil), incremental.buckets[HealthTierHealthy]...)
			incremental.mu.RUnlock()

			if len(want) != len(got) {
				t.Fatalf("桶长度 %d，期望 %d", len(got), len(want))
			}
			for i := range want {
				if want[i].dbID != got[i].dbID {
					t.Fatalf("第 %d 位是 dbID=%d，期望 %d（增量路径与 Rebuild 顺序不一致）",
						i, got[i].dbID, want[i].dbID)
				}
			}
		})
	}
}

// TestFastSchedulerRemoveKeepsPositionsConsistent 覆盖"末尾补洞"式删除：
// 摘除中间条目后，剩余账号的位置索引必须仍然指向正确的桶下标。
func TestFastSchedulerRemoveKeepsPositionsConsistent(t *testing.T) {
	const n = 20
	scheduler := NewFastScheduler(2, "round_robin")
	accounts := make([]*Account, 0, n)
	for i := 1; i <= n; i++ {
		accounts = append(accounts, newFastSchedulerTestAccount(int64(i), HealthTierHealthy, float64(i), 2))
	}
	scheduler.Rebuild(accounts)

	for _, dbID := range []int64{5, 1, 20, 13} {
		scheduler.Remove(dbID)
	}

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	entries := scheduler.buckets[HealthTierHealthy]
	if len(entries) != n-4 {
		t.Fatalf("删除 4 个后桶里剩 %d 个，期望 %d", len(entries), n-4)
	}
	if len(scheduler.positions) != len(entries) {
		t.Fatalf("positions 有 %d 项，桶里 %d 项，索引与桶不同步", len(scheduler.positions), len(entries))
	}
	for idx, entry := range entries {
		pos, ok := scheduler.positions[entry.dbID]
		if !ok {
			t.Fatalf("dbID=%d 在桶里但没有位置索引", entry.dbID)
		}
		if pos.index != idx || pos.tier != HealthTierHealthy {
			t.Fatalf("dbID=%d 位置索引是 %+v，期望 index=%d tier=%v", entry.dbID, pos, idx, HealthTierHealthy)
		}
	}
	for _, dbID := range []int64{5, 1, 20, 13} {
		if _, ok := scheduler.positions[dbID]; ok {
			t.Fatalf("dbID=%d 已被删除但位置索引还在", dbID)
		}
	}
}
