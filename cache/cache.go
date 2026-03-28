package cache

import (
	"context"
	"time"
)

// PoolStats 统一的缓存连接池状态表示。
// 对于内存缓存，这些值用于向管理后台暴露一致的观测接口。
type PoolStats struct {
	TotalConns uint32
	IdleConns  uint32
	StaleConns uint32
}

// TokenCache 统一的 token 缓存与刷新锁接口。
type TokenCache interface {
	Driver() string
	Label() string
	Close() error
	Ping(ctx context.Context) error
	Stats() PoolStats
	PoolSize() int
	SetPoolSize(n int)
	GetAccessToken(ctx context.Context, accountID int64) (string, error)
	SetAccessToken(ctx context.Context, accountID int64, token string, ttl time.Duration) error
	DeleteAccessToken(ctx context.Context, accountID int64) error
	AcquireRefreshLock(ctx context.Context, accountID int64, ttl time.Duration) (bool, error)
	ReleaseRefreshLock(ctx context.Context, accountID int64) error
	WaitForRefreshComplete(ctx context.Context, accountID int64, timeout time.Duration) (string, error)
}
