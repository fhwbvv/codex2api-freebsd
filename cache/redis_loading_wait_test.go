package cache

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var errLoading = errors.New("LOADING Redis is loading the dataset in memory")

func TestIsRedisLoadingError(t *testing.T) {
	if !isRedisLoadingError(errLoading) {
		t.Fatal("LOADING 回复应被识别为加载中错误")
	}
	if isRedisLoadingError(errors.New("NOAUTH Authentication required")) {
		t.Fatal("鉴权错误不应被识别为加载中错误")
	}
	if isRedisLoadingError(errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")) {
		t.Fatal("拒绝连接不应被识别为加载中错误")
	}
	if isRedisLoadingError(nil) {
		t.Fatal("nil 不应被识别为加载中错误")
	}
}

func TestWaitForRedisLoadingSucceedsAfterRetries(t *testing.T) {
	calls := 0
	ping := func(context.Context) error {
		calls++
		if calls <= 3 {
			return errLoading
		}
		return nil
	}
	var slept time.Duration
	sleep := func(d time.Duration) { slept += d }

	if err := waitForRedisLoading(context.Background(), ping, time.Minute, sleep); err != nil {
		t.Fatalf("加载完成后应成功: %v", err)
	}
	if calls != 4 {
		t.Fatalf("期望 4 次 ping（3 次 LOADING + 1 次成功），实际 %d", calls)
	}
	if slept == 0 {
		t.Fatal("重试之间应有等待")
	}
}

func TestWaitForRedisLoadingFailsFastOnOtherErrors(t *testing.T) {
	calls := 0
	authErr := errors.New("WRONGPASS invalid username-password pair")
	ping := func(context.Context) error {
		calls++
		return authErr
	}
	err := waitForRedisLoading(context.Background(), ping, time.Minute, func(time.Duration) {})
	if !errors.Is(err, authErr) {
		t.Fatalf("非 LOADING 错误应原样返回: %v", err)
	}
	if calls != 1 {
		t.Fatalf("非 LOADING 错误不应重试，实际 ping %d 次", calls)
	}
}

func TestWaitForRedisLoadingZeroWaitFailsFast(t *testing.T) {
	calls := 0
	ping := func(context.Context) error {
		calls++
		return errLoading
	}
	err := waitForRedisLoading(context.Background(), ping, 0, func(time.Duration) {})
	if !errors.Is(err, errLoading) {
		t.Fatalf("wait=0 时应立即返回 LOADING 错误: %v", err)
	}
	if calls != 1 {
		t.Fatalf("wait=0 不应重试，实际 ping %d 次", calls)
	}
}

func TestWaitForRedisLoadingTimesOut(t *testing.T) {
	ping := func(context.Context) error { return errLoading }
	err := waitForRedisLoading(context.Background(), ping, 10*time.Second, func(time.Duration) {})
	if err == nil {
		t.Fatal("持续 LOADING 应在超时后返回错误")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("超时错误应包含超时说明: %v", err)
	}
	if !errors.Is(err, errLoading) {
		t.Fatalf("超时错误应包裹原始 LOADING 错误: %v", err)
	}
}

func TestRedisLoadingWaitEnvOverride(t *testing.T) {
	t.Setenv("CODEX_REDIS_LOADING_WAIT_SECONDS", "")
	if got := redisLoadingWait(); got != defaultRedisLoadingWait {
		t.Fatalf("默认应为 %s，实际 %s", defaultRedisLoadingWait, got)
	}
	t.Setenv("CODEX_REDIS_LOADING_WAIT_SECONDS", "30")
	if got := redisLoadingWait(); got != 30*time.Second {
		t.Fatalf("期望 30s，实际 %s", got)
	}
	t.Setenv("CODEX_REDIS_LOADING_WAIT_SECONDS", "0")
	if got := redisLoadingWait(); got != 0 {
		t.Fatalf("0 应禁用等待，实际 %s", got)
	}
	t.Setenv("CODEX_REDIS_LOADING_WAIT_SECONDS", "abc")
	if got := redisLoadingWait(); got != defaultRedisLoadingWait {
		t.Fatalf("非法值应回退默认，实际 %s", got)
	}
}
