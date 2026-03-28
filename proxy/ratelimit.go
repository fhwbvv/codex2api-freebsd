package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// tokenBucket 简单令牌桶限流器
type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64   // 每秒补充的令牌数
	lastRefill time.Time
}

func newTokenBucket(rpm int) *tokenBucket {
	rps := float64(rpm) / 60.0
	return &tokenBucket{
		tokens:     float64(rpm),
		maxTokens:  float64(rpm),
		refillRate: rps,
		lastRefill: time.Now(),
	}
}

func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastRefill = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

func (tb *tokenBucket) updateRPM(rpm int) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	rps := float64(rpm) / 60.0
	tb.maxTokens = float64(rpm)
	tb.refillRate = rps
	tb.tokens = float64(rpm)
	tb.lastRefill = time.Now()
}

// RateLimiter 全局限流器（支持运行时动态调整）
type RateLimiter struct {
	mu      sync.RWMutex
	rpm     int
	bucket  *tokenBucket
	enabled bool
}

// NewRateLimiter 创建限流器
func NewRateLimiter(rpm int) *RateLimiter {
	rl := &RateLimiter{
		rpm:     rpm,
		enabled: rpm > 0,
	}
	if rl.enabled {
		rl.bucket = newTokenBucket(rpm)
	}
	return rl
}

// UpdateRPM 动态更新 RPM 限制
func (rl *RateLimiter) UpdateRPM(rpm int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.rpm = rpm
	rl.enabled = rpm > 0
	if rl.enabled {
		if rl.bucket == nil {
			rl.bucket = newTokenBucket(rpm)
		} else {
			rl.bucket.updateRPM(rpm)
		}
	}
}

// GetRPM 获取当前 RPM
func (rl *RateLimiter) GetRPM() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.rpm
}

// Middleware 返回 Gin 中间件
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		// 只限制代理请求，不限制管理后台和健康检查
		if path == "/health" ||
			(len(path) >= 4 && path[:4] == "/api") ||
			(len(path) >= 6 && path[:6] == "/admin") {
			c.Next()
			return
		}

		rl.mu.RLock()
		enabled := rl.enabled
		bucket := rl.bucket
		rl.mu.RUnlock()

		if enabled && bucket != nil && !bucket.allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"message": "请求过于频繁，请稍后重试",
					"type":    "rate_limit_error",
					"code":    "rate_limit_exceeded",
				},
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
