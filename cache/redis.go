package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisTokenCache Redis Token 缓存（参考 sub2api OpenAITokenCache 接口）
type redisTokenCache struct {
	client *redis.Client
}

// NewRedis 创建 Redis Token 缓存（poolSize <= 0 时使用默认值）。
func NewRedis(addr, password string, db int, poolSize ...int) (TokenCache, error) {
	opts := &redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	}
	if len(poolSize) > 0 && poolSize[0] > 0 {
		opts.PoolSize = poolSize[0]
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis 连接失败: %w", err)
	}

	return &redisTokenCache{client: client}, nil
}

// Close 关闭 Redis 连接
func (tc *redisTokenCache) Driver() string {
	return "redis"
}

func (tc *redisTokenCache) Label() string {
	return "Redis"
}

func (tc *redisTokenCache) Close() error {
	return tc.client.Close()
}

// Ping 检查 Redis 连通性
func (tc *redisTokenCache) Ping(ctx context.Context) error {
	return tc.client.Ping(ctx).Err()
}

// Stats 返回 Redis 连接池状态
func (tc *redisTokenCache) Stats() PoolStats {
	stats := tc.client.PoolStats()
	return PoolStats{
		TotalConns: stats.TotalConns,
		IdleConns:  stats.IdleConns,
		StaleConns: stats.StaleConns,
	}
}

// PoolSize 返回连接池大小配置
func (tc *redisTokenCache) PoolSize() int {
	return tc.client.Options().PoolSize
}

// SetPoolSize 设置连接池大小（go-redis 不支持运行时调整，需重启生效）
// 此方法仅保存配置值用于持久化，实际生效需重启容器
func (tc *redisTokenCache) SetPoolSize(n int) {
	// go-redis v9 的 PoolSize 在创建后不可变更
	// 此处仅做记录，重启后 main.go 会使用数据库中保存的值
	_ = n
}

// ==================== Access Token 缓存 ====================

func tokenKey(accountID int64) string {
	return fmt.Sprintf("codex:token:%d", accountID)
}

// GetAccessToken 获取缓存的 AT
func (tc *redisTokenCache) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	val, err := tc.client.Get(ctx, tokenKey(accountID)).Result()
	if err == redis.Nil {
		return "", nil // cache miss
	}
	return val, err
}

// SetAccessToken 缓存 AT
func (tc *redisTokenCache) SetAccessToken(ctx context.Context, accountID int64, token string, ttl time.Duration) error {
	return tc.client.Set(ctx, tokenKey(accountID), token, ttl).Err()
}

// DeleteAccessToken 删除缓存的 AT
func (tc *redisTokenCache) DeleteAccessToken(ctx context.Context, accountID int64) error {
	return tc.client.Del(ctx, tokenKey(accountID)).Err()
}

// ==================== 分布式刷新锁 ====================

func refreshLockKey(accountID int64) string {
	return fmt.Sprintf("codex:refresh_lock:%d", accountID)
}

// AcquireRefreshLock 获取刷新锁（防止并发刷新同一账号）
func (tc *redisTokenCache) AcquireRefreshLock(ctx context.Context, accountID int64, ttl time.Duration) (bool, error) {
	ok, err := tc.client.SetNX(ctx, refreshLockKey(accountID), "1", ttl).Result()
	return ok, err
}

// ReleaseRefreshLock 释放刷新锁
func (tc *redisTokenCache) ReleaseRefreshLock(ctx context.Context, accountID int64) error {
	return tc.client.Del(ctx, refreshLockKey(accountID)).Err()
}

// ==================== 等待锁释放 ====================

// WaitForRefreshComplete 等待另一个进程完成刷新（轮询锁 + 读取缓存）
func (tc *redisTokenCache) WaitForRefreshComplete(ctx context.Context, accountID int64, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// 检查锁是否还在
		exists, err := tc.client.Exists(ctx, refreshLockKey(accountID)).Result()
		if err != nil {
			return "", err
		}

		if exists == 0 {
			// 锁已释放，尝试读取新的 AT
			token, err := tc.GetAccessToken(ctx, accountID)
			if err != nil {
				return "", err
			}
			if token != "" {
				return token, nil
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("等待刷新超时")
}
