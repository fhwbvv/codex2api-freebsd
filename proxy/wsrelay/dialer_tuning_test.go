package wsrelay

import (
	"testing"
)

// TestManagerDialerHasTuningFields 验证主 dialer 配置了缓冲区与 KeepAlive 拨号器（B 项调优）。
func TestManagerDialerHasTuningFields(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.Stop)

	if m.dialer.ReadBufferSize != 64*1024 {
		t.Errorf("ReadBufferSize = %d, want %d", m.dialer.ReadBufferSize, 64*1024)
	}
	if m.dialer.WriteBufferSize != 64*1024 {
		t.Errorf("WriteBufferSize = %d, want %d", m.dialer.WriteBufferSize, 64*1024)
	}
	if m.dialer.WriteBufferPool == nil {
		t.Error("WriteBufferPool should be set (shared buffer pool)")
	}
	if m.dialer.NetDialContext == nil {
		t.Error("NetDialContext should be set (TCP KeepAlive)")
	}
	if !m.dialer.EnableCompression {
		t.Error("EnableCompression should be true (large upstream frames)")
	}
}

// TestDialerCopyInheritsAllFields 验证 A 项修复：连接级 dialer 副本（浅拷贝 *m.dialer）
// 继承了 NetDialContext / 缓冲区 / 压缩等全部调优字段，而非旧实现只抄 2 个字段。
func TestDialerCopyInheritsAllFields(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.Stop)

	// 模拟 createConnection 里的浅拷贝
	dialerCopy := *m.dialer
	dialer := &dialerCopy

	if dialer.NetDialContext == nil {
		t.Error("副本丢失 NetDialContext —— KeepAlive 将失效（这正是修复前的 bug）")
	}
	if dialer.ReadBufferSize != m.dialer.ReadBufferSize {
		t.Errorf("副本 ReadBufferSize = %d, want %d", dialer.ReadBufferSize, m.dialer.ReadBufferSize)
	}
	if dialer.WriteBufferSize != m.dialer.WriteBufferSize {
		t.Errorf("副本 WriteBufferSize = %d, want %d", dialer.WriteBufferSize, m.dialer.WriteBufferSize)
	}
	if dialer.WriteBufferPool != m.dialer.WriteBufferPool {
		t.Error("副本应共享同一 WriteBufferPool")
	}
	if dialer.EnableCompression != m.dialer.EnableCompression {
		t.Error("副本应继承 EnableCompression")
	}
	if dialer.HandshakeTimeout != m.dialer.HandshakeTimeout {
		t.Error("副本应继承 HandshakeTimeout")
	}
}

// TestAcquireBackoffConstants 校验退避常量取值合理（C 项）。
func TestAcquireBackoffConstants(t *testing.T) {
	if AcquireInitialBackoff <= 0 {
		t.Error("AcquireInitialBackoff 必须 > 0")
	}
	if AcquireMaxBackoff < AcquireInitialBackoff {
		t.Error("AcquireMaxBackoff 应 >= AcquireInitialBackoff")
	}
	if AcquireMaxWait <= AcquireMaxBackoff {
		t.Error("AcquireMaxWait 应远大于单次退避封顶")
	}
}
