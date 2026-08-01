package wsrelay

import (
	"time"

	"github.com/codex2api/proxy"
)

const (
	// WeakNetworkIdleTimeout 限制弱网模式下连接空闲后的复用窗口。较短窗口可减少
	// VPN、住宅代理或 NAT 已悄然回收 socket 后仍命中旧连接的概率。
	WeakNetworkIdleTimeout = 15 * time.Second

	// WeakNetworkMaxConnLifetime 定期轮换弱网链路上的长连接，避免连接在中间网络
	// 状态老化后继续被复用。
	WeakNetworkMaxConnLifetime = 3 * time.Minute
)

func weakNetworkModeEnabled() bool {
	return proxy.CurrentRuntimeSettings().CodexWSWeakNetworkMode
}

func connectionIdleTimeout() time.Duration {
	if weakNetworkModeEnabled() {
		return WeakNetworkIdleTimeout
	}
	return IdleTimeout
}

func connectionMaxLifetime() time.Duration {
	if weakNetworkModeEnabled() {
		return WeakNetworkMaxConnLifetime
	}
	return MaxConnLifetime
}
