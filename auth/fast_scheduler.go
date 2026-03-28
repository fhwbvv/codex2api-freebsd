package auth

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var fastSchedulerTierOrder = []AccountHealthTier{
	HealthTierHealthy,
	HealthTierWarm,
	HealthTierRisky,
}

type fastSchedulerEntry struct {
	acc   *Account
	dbID  int64
	score float64
}

type fastSchedulerPosition struct {
	tier  AccountHealthTier
	index int
}

// FastScheduler 是一个仅使用本地内存的调度器 POC。
// 它不在请求热路径内重算全量 score，而是直接复用 Account 上已缓存的
// HealthTier / SchedulerScore / DynamicConcurrencyLimit。
//
// 这让我们可以先验证“增量维护 + 热路径轻量化”是否值得正式接管 Store.Next。
type FastScheduler struct {
	mu        sync.RWMutex
	baseLimit int64
	buckets   map[AccountHealthTier][]fastSchedulerEntry
	positions map[int64]fastSchedulerPosition
	cursors   [3]atomic.Uint64
}

func NewFastScheduler(baseLimit int64) *FastScheduler {
	if baseLimit <= 0 {
		baseLimit = 1
	}
	return &FastScheduler{
		baseLimit: baseLimit,
		buckets: map[AccountHealthTier][]fastSchedulerEntry{
			HealthTierHealthy: nil,
			HealthTierWarm:    nil,
			HealthTierRisky:   nil,
		},
		positions: make(map[int64]fastSchedulerPosition),
	}
}

// BuildFastScheduler 用当前 Store 快照构建一个独立 scheduler。
// 该方法不会影响现有生产流量路径，只用于 POC/benchmark/灰度验证。
func (s *Store) BuildFastScheduler() *FastScheduler {
	if s == nil {
		return NewFastScheduler(1)
	}
	scheduler := NewFastScheduler(atomic.LoadInt64(&s.maxConcurrency))

	s.mu.RLock()
	accounts := make([]*Account, len(s.accounts))
	copy(accounts, s.accounts)
	s.mu.RUnlock()

	scheduler.Rebuild(accounts)
	return scheduler
}

func (s *FastScheduler) Rebuild(accounts []*Account) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.buckets = map[AccountHealthTier][]fastSchedulerEntry{
		HealthTierHealthy: nil,
		HealthTierWarm:    nil,
		HealthTierRisky:   nil,
	}
	s.positions = make(map[int64]fastSchedulerPosition, len(accounts))

	now := time.Now()
	for _, acc := range accounts {
		s.insertLocked(acc, now)
	}
}

func (s *FastScheduler) Update(acc *Account) {
	if s == nil || acc == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeLocked(acc.DBID)
	s.insertLocked(acc, time.Now())
}

func (s *FastScheduler) Remove(dbID int64) {
	if s == nil || dbID == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(dbID)
}

func (s *FastScheduler) SetBaseLimit(baseLimit int64) {
	if s == nil {
		return
	}
	if baseLimit <= 0 {
		baseLimit = 1
	}
	s.mu.Lock()
	s.baseLimit = baseLimit
	s.mu.Unlock()
}

func (s *FastScheduler) Acquire() *Account {
	if s == nil {
		return nil
	}

	now := time.Now()

	s.mu.RLock()
	baseLimit := s.baseLimit
	for tierIdx, tier := range fastSchedulerTierOrder {
		bucket := s.buckets[tier]
		if len(bucket) == 0 {
			continue
		}

		start := int(s.cursors[tierIdx].Add(1)-1) % len(bucket)
		for offset := 0; offset < len(bucket); offset++ {
			entry := bucket[(start+offset)%len(bucket)]
			if entry.acc == nil {
				continue
			}

			_, _, limit, available := entry.acc.fastSchedulerSnapshot(baseLimit, now)
			if !available || limit <= 0 {
				continue
			}
			if !tryAcquireAccount(entry.acc, limit) {
				continue
			}
			s.mu.RUnlock()
			return entry.acc
		}
	}
	s.mu.RUnlock()
	return nil
}

func (s *FastScheduler) Release(acc *Account) {
	if acc == nil {
		return
	}
	atomic.AddInt64(&acc.ActiveRequests, -1)
}

func (s *FastScheduler) BucketSizes() map[AccountHealthTier]int {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[AccountHealthTier]int{
		HealthTierHealthy: len(s.buckets[HealthTierHealthy]),
		HealthTierWarm:    len(s.buckets[HealthTierWarm]),
		HealthTierRisky:   len(s.buckets[HealthTierRisky]),
	}
}

func (s *FastScheduler) insertLocked(acc *Account, now time.Time) {
	if acc == nil || acc.DBID == 0 {
		return
	}

	tier, score, limit, available := acc.fastSchedulerSnapshot(s.baseLimit, now)
	if !available || limit <= 0 {
		return
	}
	if tier != HealthTierHealthy && tier != HealthTierWarm && tier != HealthTierRisky {
		return
	}

	entries := append(s.buckets[tier], fastSchedulerEntry{
		acc:   acc,
		dbID:  acc.DBID,
		score: score,
	})
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].score == entries[j].score {
			return entries[i].dbID < entries[j].dbID
		}
		return entries[i].score > entries[j].score
	})
	s.buckets[tier] = entries
	s.rebuildPositionsLocked(tier)
}

func (s *FastScheduler) removeLocked(dbID int64) {
	pos, ok := s.positions[dbID]
	if !ok {
		return
	}

	entries := s.buckets[pos.tier]
	if pos.index < 0 || pos.index >= len(entries) {
		delete(s.positions, dbID)
		return
	}

	copy(entries[pos.index:], entries[pos.index+1:])
	entries = entries[:len(entries)-1]
	s.buckets[pos.tier] = entries
	delete(s.positions, dbID)
	s.rebuildPositionsLocked(pos.tier)
}

func (s *FastScheduler) rebuildPositionsLocked(tier AccountHealthTier) {
	for idx, entry := range s.buckets[tier] {
		s.positions[entry.dbID] = fastSchedulerPosition{
			tier:  tier,
			index: idx,
		}
	}
}

func (a *Account) fastSchedulerSnapshot(baseLimit int64, now time.Time) (AccountHealthTier, float64, int64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	tier := a.healthTierLocked()
	score := a.SchedulerScore
	limit := a.DynamicConcurrencyLimit

	if score == 0 && tier != HealthTierBanned && a.AccessToken != "" && a.Status != StatusError {
		score = 100
	}
	if limit <= 0 {
		limit = concurrencyLimitForTier(baseLimit, tier)
	}

	available := a.Status != StatusError && tier != HealthTierBanned && a.AccessToken != ""
	if a.Status == StatusCooldown && now.Before(a.CooldownUtil) {
		available = false
	}

	return tier, score, limit, available
}

func tryAcquireAccount(acc *Account, limit int64) bool {
	if acc == nil {
		return false
	}

	if limit <= 0 {
		return false
	}

	for {
		current := atomic.LoadInt64(&acc.ActiveRequests)
		if current >= limit {
			return false
		}
		if atomic.CompareAndSwapInt64(&acc.ActiveRequests, current, current+1) {
			atomic.AddInt64(&acc.TotalRequests, 1)
			atomic.StoreInt64(&acc.LastUsedAt, time.Now().UnixNano())
			return true
		}
	}
}
