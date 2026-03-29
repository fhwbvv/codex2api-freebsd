package proxy

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============ Token Bucket Tests ============

func TestTokenBucket_BasicAllow(t *testing.T) {
	tb := newTokenBucket(60) // 60 RPM = 1 RPS

	// 初始应该允许
	if !tb.allow() {
		t.Error("Expected first allow to succeed")
	}
}

func TestTokenBucket_RateLimiting(t *testing.T) {
	tb := newTokenBucket(60) // 60 RPM = 1 RPS

	// 快速消耗所有令牌
	allowed := 0
	for i := 0; i < 70; i++ {
		if tb.allow() {
			allowed++
		}
	}

	// 应该只允许约60个（初始桶大小）
	if allowed < 50 || allowed > 65 {
		t.Errorf("Expected ~60 allowed, got %d", allowed)
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	tb := newTokenBucket(60) // 60 RPM = 1 RPS

	// 消耗一些令牌
	for i := 0; i < 10; i++ {
		tb.allow()
	}

	// 等待补充
	time.Sleep(2 * time.Second)

	// 应该能获取新令牌
	if !tb.allow() {
		t.Error("Expected token refill after wait")
	}
}

func TestTokenBucket_UpdateRPM(t *testing.T) {
	tb := newTokenBucket(60)

	// 更新到更高的RPM
	tb.updateRPM(120)

	if tb.maxTokens != 120 {
		t.Errorf("Expected maxTokens=120, got %f", tb.maxTokens)
	}

	// 更新到0（禁用）
	tb.updateRPM(0)
	if tb.maxTokens != 0 {
		t.Errorf("Expected maxTokens=0, got %f", tb.maxTokens)
	}
}

func TestTokenBucket_AllowN(t *testing.T) {
	tb := newTokenBucket(60)

	// 请求5个令牌
	if !tb.allowN(5) {
		t.Error("Expected allowN(5) to succeed")
	}

	// 请求超过剩余令牌数
	if tb.allowN(100) {
		t.Error("Expected allowN(100) to fail after consuming 5")
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	tb := newTokenBucket(1000)

	var wg sync.WaitGroup
	allowed := int64(0)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if tb.allow() {
					// 使用原子操作
					atomic.AddInt64(&allowed, 1)
				}
			}
		}()
	}

	wg.Wait()
	// 主要验证无竞态条件
}

// ============ Cooldown Manager Tests ============

func TestCooldownManager_Basic(t *testing.T) {
	cm := newCooldownManager()

	if cm.isInCooldown() {
		t.Error("Expected no cooldown initially")
	}

	// 进入冷却
	duration := cm.enterCooldown()
	if duration <= 0 {
		t.Error("Expected positive cooldown duration")
	}

	if !cm.isInCooldown() {
		t.Error("Expected to be in cooldown after entering")
	}

	// 验证等级增加
	level, _, _ := cm.getState()
	if level != 0 {
		t.Errorf("Expected level=0, got %d", level)
	}
}

func TestCooldownManager_ExponentialBackoff(t *testing.T) {
	cm := newCooldownManager()

	expectedDurations := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	}

	for i, expected := range expectedDurations {
		cm.reset()
		// 模拟多次进入冷却以达到第i级
		// 每次 enterCooldown 会先调用 computeCooldown，level 会增加
		// 所以为了达到 level i，我们需要调用 i 次
		for j := 0; j < i; j++ {
			cm.enterCooldown()
		}

		duration, level := cm.computeCooldown()
		if duration != expected {
			t.Errorf("Iteration %d: Expected duration=%v, got %v", i, expected, duration)
		}
		if level != i {
			t.Errorf("Iteration %d: Expected level=%d, got %d", i, i, level)
		}
	}
}

func TestCooldownManager_MaxLevel(t *testing.T) {
	cm := newCooldownManager()

	// 尝试超过最大等级
	for i := 0; i < len(cooldownDurations) + 10; i++ {
		cm.enterCooldown()
	}

	level, _, _ := cm.getState()
	maxLevel := len(cooldownDurations) - 1
	if level != maxLevel {
		t.Errorf("Expected max level=%d, got %d", maxLevel, level)
	}
}

func TestCooldownManager_Reset(t *testing.T) {
	cm := newCooldownManager()

	// 进入冷却
	cm.enterCooldown()
	if !cm.isInCooldown() {
		t.Fatal("Expected to be in cooldown")
	}

	// 重置
	cm.reset()
	if cm.isInCooldown() {
		t.Error("Expected no cooldown after reset")
	}

	level, _, _ := cm.getState()
	if level != -1 {
		t.Errorf("Expected level=-1 after reset, got %d", level)
	}
}

func TestCooldownManager_Remaining(t *testing.T) {
	cm := newCooldownManager()

	// 进入短冷却
	cm.enterCooldown()
	remaining := cm.getRemainingCooldown()
	if remaining <= 0 {
		t.Error("Expected positive remaining cooldown")
	}

	// 重置后应该为0
	cm.reset()
	remaining = cm.getRemainingCooldown()
	if remaining != 0 {
		t.Errorf("Expected 0 remaining after reset, got %v", remaining)
	}
}

// ============ Level Limiter Tests ============

func TestLevelLimiter_Basic(t *testing.T) {
	ll := newLevelLimiter("test", LevelGlobal, 60)

	// 应该允许请求
	if !ll.allow() {
		t.Error("Expected allow to succeed")
	}

	// 检查指标
	metrics := ll.getMetrics()
	if metrics.TotalRequests != 1 {
		t.Errorf("Expected TotalRequests=1, got %d", metrics.TotalRequests)
	}
	if metrics.AllowedRequests != 1 {
		t.Errorf("Expected AllowedRequests=1, got %d", metrics.AllowedRequests)
	}
}

func TestLevelLimiter_Cooldown(t *testing.T) {
	ll := newLevelLimiter("test", LevelGlobal, 1) // 1 RPM，容易触发限流

	// 第一个请求应该通过
	if !ll.allow() {
		t.Error("Expected first allow to succeed")
	}

	// 第二个请求应该触发冷却
	if ll.allow() {
		t.Error("Expected second allow to fail due to rate limit")
	}

	// 检查是否在冷却中
	if !ll.cooldown.isInCooldown() {
		t.Error("Expected to be in cooldown")
	}

	// 冷却期间应该持续拒绝
	if ll.allow() {
		t.Error("Expected allow to fail during cooldown")
	}
}

func TestLevelLimiter_UpdateRPM(t *testing.T) {
	ll := newLevelLimiter("test", LevelGlobal, 60)

	// 更新RPM
	ll.updateRPM(120)

	metrics := ll.getMetrics()
	if metrics.LimitRPM != 120 {
		t.Errorf("Expected LimitRPM=120, got %d", metrics.LimitRPM)
	}

	// 禁用
	ll.updateRPM(0)
	if !ll.allow() {
		t.Error("Expected allow to succeed when disabled")
	}
}

func TestLevelLimiter_ResetCooldown(t *testing.T) {
	ll := newLevelLimiter("test", LevelGlobal, 1)

	// 触发冷却
	ll.allow()
	ll.allow() // 触发限流

	if !ll.cooldown.isInCooldown() {
		t.Fatal("Expected to be in cooldown")
	}

	// 重置冷却
	ll.resetCooldown()
	if ll.cooldown.isInCooldown() {
		t.Error("Expected cooldown to be reset")
	}
}

func TestLevelLimiter_Snapshot(t *testing.T) {
	ll := newLevelLimiter("test-key", LevelAccount, 60)

	// 触发一些请求
	ll.allow()
	ll.allow()

	snapshot := ll.getSnapshot()
	if snapshot.Key != "test-key" {
		t.Errorf("Expected Key='test-key', got %s", snapshot.Key)
	}
	if snapshot.Level != "account" {
		t.Errorf("Expected Level='account', got %s", snapshot.Level)
	}
	if snapshot.LimitRPM != 60 {
		t.Errorf("Expected LimitRPM=60, got %d", snapshot.LimitRPM)
	}
}

func TestLevelLimiter_Metrics(t *testing.T) {
	ll := newLevelLimiter("test", LevelGlobal, 60)

	// 触发限流
	for i := 0; i < 100; i++ {
		ll.allow()
	}

	metrics := ll.getMetrics()
	if metrics.TotalRequests <= 0 {
		t.Error("Expected positive TotalRequests")
	}
	if metrics.BlockedRequests <= 0 {
		t.Error("Expected positive BlockedRequests after triggering limit")
	}

	// 验证更新时间
	if metrics.LastUpdatedAt.IsZero() {
		t.Error("Expected LastUpdatedAt to be set")
	}
}

// ============ Enhanced Rate Limiter Tests ============

func TestEnhancedRateLimiter_Basic(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 100, 50, 30)
	defer erl.Stop()

	// 全局应该允许
	if !erl.Allow() {
		t.Error("Expected global allow to succeed")
	}
}

func TestEnhancedRateLimiter_AllowWithContext(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 1000, 100, 50)
	defer erl.Stop()

	// 带上下文的请求
	if !erl.AllowWithContext("account1", "gpt-5.4") {
		t.Error("Expected allow with context to succeed")
	}
}

func TestEnhancedRateLimiter_MultiLevel(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 100, 10, 5)
	defer erl.Stop()

	// 快速触发账号级限流
	account := "test-account"
	allowed := 0
	for i := 0; i < 20; i++ {
		if erl.AllowWithContext(account, "") {
			allowed++
		}
	}

	// 应该有一些被限流
	if allowed >= 20 {
		t.Error("Expected some requests to be rate limited")
	}

	// 检查账号级指标
	metrics := erl.GetAccountMetrics(account)
	if metrics.TotalRequests == 0 {
		t.Error("Expected non-zero TotalRequests for account")
	}
}

func TestEnhancedRateLimiter_UpdateRPM(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 60, 30, 15)
	defer erl.Stop()

	// 更新全局RPM
	erl.UpdateGlobalRPM(120)
	if erl.globalRPM != 120 {
		t.Errorf("Expected globalRPM=120, got %d", erl.globalRPM)
	}

	// 更新账号RPM
	erl.UpdateAccountRPM(60)
	if erl.accountRPM != 60 {
		t.Errorf("Expected accountRPM=60, got %d", erl.accountRPM)
	}

	// 更新模型RPM
	erl.UpdateModelRPM(30)
	if erl.modelRPM != 30 {
		t.Errorf("Expected modelRPM=30, got %d", erl.modelRPM)
	}
}

func TestEnhancedRateLimiter_UpdateAllRPM(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 60, 30, 15)
	defer erl.Stop()

	// 一次性更新所有
	erl.UpdateAllRPM(120, 60, 30)

	if erl.globalRPM != 120 {
		t.Errorf("Expected globalRPM=120, got %d", erl.globalRPM)
	}
	if erl.accountRPM != 60 {
		t.Errorf("Expected accountRPM=60, got %d", erl.accountRPM)
	}
	if erl.modelRPM != 30 {
		t.Errorf("Expected modelRPM=30, got %d", erl.modelRPM)
	}
}

func TestEnhancedRateLimiter_Metrics(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 100, 50, 25)
	defer erl.Stop()

	// 触发一些请求
	for i := 0; i < 10; i++ {
		erl.AllowWithContext("acc1", "model1")
	}

	// 获取全局指标
	globalMetrics := erl.GetGlobalMetrics()
	if globalMetrics.TotalRequests == 0 {
		t.Error("Expected non-zero global TotalRequests")
	}

	// 获取账号指标
	accMetrics := erl.GetAccountMetrics("acc1")
	if accMetrics.TotalRequests == 0 {
		t.Error("Expected non-zero account TotalRequests")
	}

	// 获取模型指标
	modelMetrics := erl.GetModelMetrics("model1")
	if modelMetrics.TotalRequests == 0 {
		t.Error("Expected non-zero model TotalRequests")
	}

	// 获取所有指标
	allMetrics := erl.GetAllMetrics()
	if _, ok := allMetrics["global"]; !ok {
		t.Error("Expected 'global' in all metrics")
	}
	if _, ok := allMetrics["accounts"]; !ok {
		t.Error("Expected 'accounts' in all metrics")
	}
	if _, ok := allMetrics["models"]; !ok {
		t.Error("Expected 'models' in all metrics")
	}
}

func TestEnhancedRateLimiter_Snapshots(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 100, 50, 25)
	defer erl.Stop()

	// 创建一些限流器
	erl.AllowWithContext("acc1", "model1")
	erl.AllowWithContext("acc2", "model2")

	snapshots := erl.GetAllSnapshots()
	if len(snapshots) == 0 {
		t.Error("Expected non-empty snapshots")
	}

	// 应该包含全局
	foundGlobal := false
	for _, s := range snapshots {
		if s.Level == "global" {
			foundGlobal = true
			break
		}
	}
	if !foundGlobal {
		t.Error("Expected global snapshot")
	}
}

func TestEnhancedRateLimiter_Disabled(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 0, 0, 0)
	defer erl.Stop()

	// 禁用状态下应该总是允许
	if !erl.Allow() {
		t.Error("Expected allow to succeed when disabled")
	}
	if !erl.AllowWithContext("acc", "model") {
		t.Error("Expected allow with context to succeed when disabled")
	}
}

func TestEnhancedRateLimiter_Concurrent(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 10000, 1000, 500)
	defer erl.Stop()

	var wg sync.WaitGroup

	// 并发请求
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			account := fmt.Sprintf("account%d", idx%10)
			model := fmt.Sprintf("model%d", idx%5)
			for j := 0; j < 10; j++ {
				erl.AllowWithContext(account, model)
			}
		}(i)
	}

	wg.Wait()

	// 验证指标
	metrics := erl.GetAllMetrics()
	globalMetrics := metrics["global"].(LimitMetrics)
	if globalMetrics.TotalRequests == 0 {
		t.Error("Expected non-zero global TotalRequests after concurrent access")
	}
}

// ============ Backward Compatibility Tests ============

func TestRateLimiter_BackwardCompatible(t *testing.T) {
	rl := NewRateLimiter(60)

	// 基本功能测试
	if rl.GetRPM() != 60 {
		t.Errorf("Expected RPM=60, got %d", rl.GetRPM())
	}

	// 更新RPM
	rl.UpdateRPM(120)
	if rl.GetRPM() != 120 {
		t.Errorf("Expected RPM=120 after update, got %d", rl.GetRPM())
	}

	// 检查增强型限流器
	enhanced := rl.GetEnhancedLimiter()
	if enhanced == nil {
		t.Error("Expected non-nil enhanced limiter")
	}
}

func TestRateLimiter_Middleware(t *testing.T) {
	// 这里我们只测试中间件存在且不panic
	// 完整测试需要Gin框架
	rl := NewRateLimiter(1000)
	_ = rl.Middleware()
	// 如果到这里没有panic，测试通过
}

// ============ ComputeCooldown Tests ============

func TestComputeCooldown(t *testing.T) {
	tests := []struct {
		prevLevel     int
		expectedDur   time.Duration
		expectedLevel int
	}{
		{-1, 1 * time.Second, 0},
		{0, 2 * time.Second, 1},
		{1, 4 * time.Second, 2},
		{5, 64 * time.Second, 6},
		{100, 1800 * time.Second, 11}, // 超过最大等级
	}

	for _, tt := range tests {
		dur, level := ComputeCooldown(tt.prevLevel)
		if dur != tt.expectedDur {
			t.Errorf("prevLevel=%d: expected duration=%v, got %v", tt.prevLevel, tt.expectedDur, dur)
		}
		if level != tt.expectedLevel {
			t.Errorf("prevLevel=%d: expected level=%d, got %d", tt.prevLevel, tt.expectedLevel, level)
		}
	}
}

// ============ RateLimitError Tests ============

func TestRateLimitError(t *testing.T) {
	err := &RateLimitError{
		Level:      LevelGlobal,
		Key:        "test",
		RetryAfter: 5 * time.Second,
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("Expected non-empty error string")
	}

	if err.HTTPStatusCode() != http.StatusTooManyRequests {
		t.Errorf("Expected status %d, got %d", http.StatusTooManyRequests, err.HTTPStatusCode())
	}
}
