package main

import (
	"sync"
	"time"
)

// healthStoreLockStallThreshold 是 /health 判定账号池疑似死锁的门槛:
// 单次 TryRLock 失败只是正常锁竞争,不能作为重启依据;但连续这么久一次
// 都拿不到读锁,说明有写锁永久持有,实例已无法服务任何请求。此时 /health
// 返回 503,让容器编排的 healthcheck 重启实例——恢复旧版同步 /health
// 死锁时挂起超时带来的自愈能力,同时不误伤高负载下的瞬时竞争。
const healthStoreLockStallThreshold = 30 * time.Second

// healthLockProbe 跟踪 /health 连续拿不到账号池读锁的持续时长。
// 任何一次成功探测都会清零;只有从首次失败起一路无成功才会累计。
type healthLockProbe struct {
	mu           sync.Mutex
	blockedSince time.Time
}

// observe 记录一次探测结果,返回当前连续失败的持续时长(成功时为 0)。
func (p *healthLockProbe) observe(lockAcquired bool, now time.Time) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if lockAcquired {
		p.blockedSince = time.Time{}
		return 0
	}
	if p.blockedSince.IsZero() {
		p.blockedSince = now
	}
	return now.Sub(p.blockedSince)
}
