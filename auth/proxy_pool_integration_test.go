package auth

import (
	"context"
	"testing"
	"time"
)

// MockDB 用于测试的 mock 数据库
type MockDB struct {
	proxies []*MockProxyRow
}

type MockProxyRow struct {
	ID            int64
	URL           string
	Label         string
	Enabled       bool
	TestIP        string
	TestLocation  string
	TestLatencyMs int
}

func (m *MockDB) ListEnabledProxies(ctx context.Context) ([]*MockProxyRow, error) {
	var result []*MockProxyRow
	for _, p := range m.proxies {
		if p.Enabled {
			result = append(result, p)
		}
	}
	return result, nil
}

func TestNewEnhancedProxyPool(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	if pool == nil {
		t.Fatal("NewEnhancedProxyPool() returned nil")
	}
	if pool.pool == nil {
		t.Error("internal pool should not be nil")
	}
}

func TestEnhancedProxyPoolInit(t *testing.T) {
	// 这个测试在没有真实数据库的情况下需要 mock
	// 这里简化测试
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	// 手动添加代理
	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.pool.AddProxy("http://proxy2:8080", 5)
	pool.initialized.Store(true)
	pool.enabled.Store(true)

	if !pool.Enabled() {
		t.Error("pool should be enabled")
	}

	if pool.pool.Size() != 2 {
		t.Errorf("expected 2 proxies, got %d", pool.pool.Size())
	}
}

func TestEnhancedProxyPoolNextProxy(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	// 未初始化应该返回空
	if pool.NextProxy() != "" {
		t.Error("NextProxy should return empty when not initialized")
	}

	// 初始化
	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)
	pool.enabled.Store(true)

	url := pool.NextProxy()
	if url == "" {
		t.Error("NextProxy should return a proxy URL")
	}
}

func TestEnhancedProxyPoolSelectWithStrategy(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	pool.pool.AddProxy("http://proxy1:8080", 100)
	pool.pool.AddProxy("http://proxy2:8080", 1)
	pool.initialized.Store(true)
	pool.enabled.Store(true)

	// 测试加权策略
	entry := pool.SelectWithStrategy(StrategyWeighted)
	if entry == nil {
		t.Fatal("SelectWithStrategy should return an entry")
	}
}

func TestEnhancedProxyPoolMarkSuccess(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)

	pool.MarkProxySuccess("http://proxy1:8080")

	stats := pool.GetStats()
	if stats["http://proxy1:8080"].TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", stats["http://proxy1:8080"].TotalRequests)
	}
}

func TestEnhancedProxyPoolMarkFailure(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)

	// 多次失败
	for i := 0; i < 5; i++ {
		pool.MarkProxyFailure("http://proxy1:8080")
	}

	stats := pool.GetStats()
	if stats["http://proxy1:8080"].FailedRequests != 5 {
		t.Errorf("expected 5 failures, got %d", stats["http://proxy1:8080"].FailedRequests)
	}
}

func TestEnhancedProxyPoolConnection(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)

	// 获取连接
	if !pool.AcquireConnection("http://proxy1:8080") {
		t.Error("AcquireConnection should succeed")
	}

	stats := pool.GetStats()
	if stats["http://proxy1:8080"].ActiveConns != 1 {
		t.Errorf("expected 1 active connection, got %d", stats["http://proxy1:8080"].ActiveConns)
	}

	// 释放连接
	pool.ReleaseConnection("http://proxy1:8080")

	stats = pool.GetStats()
	if stats["http://proxy1:8080"].ActiveConns != 0 {
		t.Errorf("expected 0 active connections, got %d", stats["http://proxy1:8080"].ActiveConns)
	}
}

func TestEnhancedProxyPoolGetStats(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	// 未初始化应该返回空 map
	stats := pool.GetStats()
	if stats == nil {
		t.Error("GetStats should not return nil")
	}

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)

	stats = pool.GetStats()
	if len(stats) != 1 {
		t.Errorf("expected 1 stat entry, got %d", len(stats))
	}
}

func TestEnhancedProxyPoolGetPoolStatus(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	// 未初始化
	status := pool.GetPoolStatus()
	if status.Total != 0 {
		t.Errorf("expected total 0, got %d", status.Total)
	}

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.pool.AddProxy("http://proxy2:8080", 5)
	pool.initialized.Store(true)

	status = pool.GetPoolStatus()
	if status.Total != 2 {
		t.Errorf("expected total 2, got %d", status.Total)
	}
}

func TestEnhancedProxyPoolSetStrategy(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)

	pool.SetStrategy(StrategyWeighted)

	status := pool.GetPoolStatus()
	if status.Strategy != "weighted" {
		t.Errorf("expected strategy weighted, got %s", status.Strategy)
	}
}

func TestEnhancedProxyPoolGetHealthyProxies(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.pool.AddProxy("http://proxy2:8080", 5)
	pool.initialized.Store(true)

	healthy := pool.GetHealthyProxies()
	if len(healthy) != 2 {
		t.Errorf("expected 2 healthy proxies, got %d", len(healthy))
	}
}

func TestEnhancedProxyPoolHasHealthyProxy(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	// 未初始化
	if pool.HasHealthyProxy() {
		t.Error("HasHealthyProxy should return false when not initialized")
	}

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)

	if !pool.HasHealthyProxy() {
		t.Error("HasHealthyProxy should return true when healthy proxies exist")
	}
}

func TestEnhancedProxyPoolSetEnabled(t *testing.T) {
	config := DefaultProxyPoolConfig()
	pool := NewEnhancedProxyPool(nil, config)

	if pool.Enabled() {
		t.Error("pool should be disabled by default")
	}

	pool.SetEnabled(true)
	if !pool.Enabled() {
		t.Error("pool should be enabled after SetEnabled(true)")
	}

	pool.SetEnabled(false)
	if pool.Enabled() {
		t.Error("pool should be disabled after SetEnabled(false)")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"10s", 10 * time.Second},
		{"5m", 5 * time.Minute},
		{"1h", time.Hour},
		{"invalid", 30 * time.Second}, // 默认值
	}

	for _, test := range tests {
		result := parseDuration(test.input, 30*time.Second)
		if result != test.expected {
			t.Errorf("parseDuration(%s) = %v, want %v", test.input, result, test.expected)
		}
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"10", 10},
		{"3", 3},
		{"0", 3}, // 默认值
		{"invalid", 3},
		{"", 3},
	}

	for _, test := range tests {
		result := parseInt(test.input, 3)
		if result != test.expected {
			t.Errorf("parseInt(%s) = %d, want %d", test.input, result, test.expected)
		}
	}
}

func TestEnhancedProxyPoolStop(t *testing.T) {
	config := DefaultProxyPoolConfig()
	config.CheckInterval = 1 * time.Hour // 设置较长间隔避免实际触发
	pool := NewEnhancedProxyPool(nil, config)

	pool.pool.AddProxy("http://proxy1:8080", 10)
	pool.initialized.Store(true)

	// 停止不应该 panic
	pool.Stop()
}
