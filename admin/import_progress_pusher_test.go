package admin

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestImportProgressPusherStopsOnWriteFailure 锁定：下游断开（写返回 broken pipe）
// 之后推送协程立刻收摊。修复前它会一直按间隔写死连接，每次刷一条
// "写入 SSE 事件失败: broken pipe"，直到整个导入结束。
func TestImportProgressPusherStopsOnWriteFailure(t *testing.T) {
	var sends int64
	done := make(chan struct{})
	defer close(done)

	stopped := runImportProgressPusher(
		context.Background(), done, time.Millisecond,
		func() importEvent { return importEvent{Type: "progress"} },
		func(importEvent) bool {
			atomic.AddInt64(&sends, 1)
			return false // 下游已断开
		},
	)

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("写失败后推送协程没有退出")
	}

	// 再等几个间隔，确认没有继续写。
	got := atomic.LoadInt64(&sends)
	time.Sleep(50 * time.Millisecond)
	if now := atomic.LoadInt64(&sends); now != got {
		t.Fatalf("退出后仍在写：%d -> %d", got, now)
	}
	if got != 1 {
		t.Fatalf("写失败后又写了 %d 次，期望 1 次就停", got)
	}
}

// TestImportProgressPusherStopsOnClientDisconnect 客户端断开（请求 ctx 取消）
// 同样要停，不必等到写失败。
func TestImportProgressPusherStopsOnClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer close(done)

	stopped := runImportProgressPusher(
		ctx, done, time.Hour, // 间隔故意设很大：只能靠 ctx 取消退出
		func() importEvent { return importEvent{Type: "progress"} },
		func(importEvent) bool { return true },
	)
	cancel()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("客户端断开后推送协程没有退出")
	}
}

// TestImportProgressPusherStopSignalGatesFinalWrite 导入结束后，调用方能等到推送
// 协程真正退出再写 complete 事件——两边并发写同一个 ResponseWriter 会让事件交错，
// 前端收不到完整的 complete，进度条卡在最后一个百分比。
func TestImportProgressPusherStopSignalGatesFinalWrite(t *testing.T) {
	var writing int64
	var overlapped int64
	done := make(chan struct{})

	stopped := runImportProgressPusher(
		context.Background(), done, time.Millisecond,
		func() importEvent { return importEvent{Type: "progress"} },
		func(importEvent) bool {
			if atomic.AddInt64(&writing, 1) != 1 {
				atomic.AddInt64(&overlapped, 1)
			}
			time.Sleep(2 * time.Millisecond) // 模拟一次慢写
			atomic.AddInt64(&writing, -1)
			return true
		},
	)

	time.Sleep(20 * time.Millisecond)
	close(done)
	<-stopped

	// 收尾写：此刻推送协程已退出，不可能重叠。
	if atomic.AddInt64(&writing, 1) != 1 {
		t.Fatal("收尾事件与进度事件并发写了同一个 ResponseWriter")
	}
	atomic.AddInt64(&writing, -1)
	if overlapped != 0 {
		t.Fatalf("推送协程内部出现 %d 次重叠写", overlapped)
	}
}
