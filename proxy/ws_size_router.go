package proxy

import (
	"os"
	"strings"
	"sync"
	"time"
)

// websocketSizeRouter 从实际发生的上游 close 1009 (message too big) 中学习
// 触发体积,让后续同量级请求直接首发 HTTP 上游,省去"先等 WS 必败、再串行
// 降级 HTTP"叠加出的首字长尾(issue #404 的核心痛点)。上游帧上限未公开,
// 这里不做静态猜测,只记录本进程观测到的最小失败体积。
type websocketSizeRouter struct {
	mu        sync.Mutex
	minTooBig int
	learnedAt time.Time
}

// wsSizeRouterMinSample 学习样本下限:真实上游帧上限远高于此,更小的
// 1009 几乎必然是误分类;同时避免异常小样本长期劫持路由。
const wsSizeRouterMinSample = 64 * 1024

// wsSizeRouterTTL 学习结果有效期:上游帧上限可能随部署调整,过期后
// 回到"先试 WS"的默认行为,由下一次真实 1009 重新校准。
const wsSizeRouterTTL = 6 * time.Hour

// wsSizeRouterMarginPercent 判定余量:请求体与实际 WS 帧之间有信封字段、
// 注入字段的少量差异,略小于已知失败体积的请求同样大概率 1009。
const wsSizeRouterMarginPercent = 95

var globalWSSizeRouter websocketSizeRouter

// wsSizeRouterDisabled 系统设置 codex_ws_size_router_enabled 关闭、或环境变量
// 逃生阀 CODEX_WS_SIZE_ROUTER=off 时,恢复"一律先试 WS"的旧行为。
func wsSizeRouterDisabled() bool {
	if !CurrentRuntimeSettings().CodexWSSizeRouter {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CODEX_WS_SIZE_ROUTER")), "off")
}

// RecordMessageTooBig 记录一次 close 1009 发生时的请求体大小。
func (r *websocketSizeRouter) RecordMessageTooBig(bodySize int) {
	if bodySize < wsSizeRouterMinSample || wsSizeRouterDisabled() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.minTooBig == 0 || bodySize < r.minTooBig {
		r.minTooBig = bodySize
	}
	r.learnedAt = time.Now()
}

// PreferHTTP 判断该体积的请求是否应跳过 WebSocket 直接走 HTTP 上游。
func (r *websocketSizeRouter) PreferHTTP(bodySize int) bool {
	if wsSizeRouterDisabled() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.minTooBig == 0 {
		return false
	}
	if time.Since(r.learnedAt) > wsSizeRouterTTL {
		r.minTooBig = 0
		return false
	}
	return bodySize >= r.minTooBig*wsSizeRouterMarginPercent/100
}
