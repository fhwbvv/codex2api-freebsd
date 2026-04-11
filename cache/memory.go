package cache

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type memoryTokenEntry struct {
	token     string
	expiresAt time.Time
}

// MemoryTokenCache 为单机轻量部署提供进程内 token 缓存和刷新锁。
// 重启后缓存丢失属于预期行为。
type MemoryTokenCache struct {
	mu       sync.RWMutex
	tokens   map[int64]memoryTokenEntry
	locks    map[int64]time.Time
	poolSize int
}

// NewMemory 创建内存缓存实现。
func NewMemory(poolSize int) TokenCache {
	if poolSize <= 0 {
		poolSize = 1
	}
	tc := &MemoryTokenCache{
		tokens:   make(map[int64]memoryTokenEntry),
		locks:    make(map[int64]time.Time),
		poolSize: poolSize,
	}
	// 启动后台定时清理过期 token 和过期锁，防止已删除账号的条目永驻内存
	go tc.cleanupLoop()
	return tc
}

// cleanupLoop 每 60 秒清理一次过期 token 和过期锁
func (tc *MemoryTokenCache) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		tc.mu.Lock()
		for id, entry := range tc.tokens {
			if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
				delete(tc.tokens, id)
			}
		}
		for id, until := range tc.locks {
			if now.After(until) {
				delete(tc.locks, id)
			}
		}
		tc.mu.Unlock()
	}
}

func (tc *MemoryTokenCache) Driver() string {
	return "memory"
}

func (tc *MemoryTokenCache) Label() string {
	return "Memory"
}

func (tc *MemoryTokenCache) Close() error {
	return nil
}

func (tc *MemoryTokenCache) Ping(ctx context.Context) error {
	return nil
}

func (tc *MemoryTokenCache) Stats() PoolStats {
	return PoolStats{
		TotalConns: 1,
		IdleConns:  1,
		StaleConns: 0,
	}
}

func (tc *MemoryTokenCache) PoolSize() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.poolSize
}

func (tc *MemoryTokenCache) SetPoolSize(n int) {
	if n <= 0 {
		n = 1
	}
	tc.mu.Lock()
	tc.poolSize = n
	tc.mu.Unlock()
}

func (tc *MemoryTokenCache) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	tc.mu.RLock()
	entry, ok := tc.tokens[accountID]
	tc.mu.RUnlock()
	if !ok {
		return "", nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		// 同步删除过期条目，避免删除刚刷新的 token
		tc.mu.Lock()
		current, ok := tc.tokens[accountID]
		if ok && current == entry && !current.expiresAt.IsZero() && time.Now().After(current.expiresAt) {
			delete(tc.tokens, accountID)
		}
		tc.mu.Unlock()
		return "", nil
	}
	return entry.token, nil
}

func (tc *MemoryTokenCache) SetAccessToken(ctx context.Context, accountID int64, token string, ttl time.Duration) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	} else if ttl < 0 {
		// 负数 TTL 表示已经过期，直接设置为过去的时间
		expiresAt = time.Now().Add(ttl)
	}
	tc.tokens[accountID] = memoryTokenEntry{
		token:     token,
		expiresAt: expiresAt,
	}
	return nil
}

func (tc *MemoryTokenCache) DeleteAccessToken(ctx context.Context, accountID int64) error {
	tc.mu.Lock()
	delete(tc.tokens, accountID)
	tc.mu.Unlock()
	return nil
}

func (tc *MemoryTokenCache) AcquireRefreshLock(ctx context.Context, accountID int64, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()

	if until, ok := tc.locks[accountID]; ok && time.Now().Before(until) {
		return false, nil
	}
	tc.locks[accountID] = time.Now().Add(ttl)
	return true, nil
}

func (tc *MemoryTokenCache) ReleaseRefreshLock(ctx context.Context, accountID int64) error {
	tc.mu.Lock()
	delete(tc.locks, accountID)
	tc.mu.Unlock()
	return nil
}

func (tc *MemoryTokenCache) WaitForRefreshComplete(ctx context.Context, accountID int64, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	deadline := time.Now().Add(timeout)
	pollTimer := time.NewTimer(200 * time.Millisecond)
	defer pollTimer.Stop()

	for time.Now().Before(deadline) {
		tc.mu.Lock()
		lockUntil, locked := tc.locks[accountID]
		if locked && time.Now().After(lockUntil) {
			delete(tc.locks, accountID)
			locked = false
		}
		entry, hasToken := tc.tokens[accountID]
		if hasToken && !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
			delete(tc.tokens, accountID)
			hasToken = false
			entry = memoryTokenEntry{}
		}
		tc.mu.Unlock()

		if !locked && hasToken && entry.token != "" {
			return entry.token, nil
		}

		pollTimer.Reset(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-pollTimer.C:
		}
	}

	return "", fmt.Errorf("等待刷新超时")
}
