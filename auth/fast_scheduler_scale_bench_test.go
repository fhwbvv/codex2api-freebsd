package auth

import (
	"strconv"
	"testing"
)

// BenchmarkStoreAddAccount 量化"往已有号池里加一个账号"的代价随号池大小的变化。
// 这条路径持有 Store 全局写锁，所有请求（调度取号、账号列表）都要排在它后面，
// 所以它必须与号池大小无关——否则批量导入会把整个网关卡住。
func BenchmarkStoreAddAccount(b *testing.B) {
	for _, pool := range []int{1000, 5000, 20000} {
		b.Run("pool="+strconv.Itoa(pool), func(b *testing.B) {
			store := newBenchStore(pool)
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				store.AddAccount(newFastSchedulerTestAccount(int64(pool+i+1), HealthTierHealthy, float64(i%97), 2))
			}
		})
	}
}

// BenchmarkFastSchedulerUpdate 量化稳态下的单次调度器条目更新（每次请求 Release、
// 每次用量刷新、每次冷却标记都会走这里）。同样必须与号池大小无关。
func BenchmarkFastSchedulerUpdate(b *testing.B) {
	for _, pool := range []int{1000, 5000, 20000} {
		b.Run("pool="+strconv.Itoa(pool), func(b *testing.B) {
			store := newBenchStore(pool)
			scheduler := store.getFastScheduler()
			hot := store.accounts[pool/2]
			b.ResetTimer()
			for b.Loop() {
				scheduler.Update(hot)
			}
		})
	}
}

func newBenchStore(n int) *Store {
	store := &Store{}
	for i := 1; i <= n; i++ {
		store.accounts = append(store.accounts, newFastSchedulerTestAccount(int64(i), HealthTierHealthy, float64(i%97), 2))
	}
	store.rebuildAccountIndex()
	store.SetFastSchedulerEnabled(true)
	store.getFastScheduler()
	return store
}
