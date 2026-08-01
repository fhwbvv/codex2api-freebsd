package proxy

import (
	"testing"

	"github.com/codex2api/auth"
)

// issue #446：维护类旁路请求（wham 用量/重置券、模型清单、alpha search、订阅同步）
// 过去每次调用都新建一次性 transport。uTLS 模式下这些 transport 用完即弃、
// 其名下的 HTTP/2 连接无人关闭，后台探针与 /models 会把连接数、goroutine
// 与内存持续推高。以下测试锁死「复用同一个池化 Client」这一不变量。

func TestGetCodexMaintenanceClientIsReusedPerAccount(t *testing.T) {
	t.Setenv("CODEX_TRANSPORT_MODE", "utls_chrome")
	clearMaintenanceClients(t)

	acc := &auth.Account{DBID: 4460}

	first := getCodexMaintenanceClient(acc, "")
	second := getCodexMaintenanceClient(acc, "")

	if first != second {
		t.Fatal("同一账号两次取用返回了不同 Client：一次性 transport 会持续泄漏连接与 goroutine")
	}
	if _, ok := first.Transport.(*utlsRoundTripper); !ok {
		t.Fatalf("utls_chrome 模式下 transport = %T, want *utlsRoundTripper", first.Transport)
	}
}

// 账号之间必须隔离：同一条 TCP 连接被不同 token 复用会被上游识别。
func TestGetCodexMaintenanceClientIsolatesAccountsAndProxies(t *testing.T) {
	t.Setenv("CODEX_TRANSPORT_MODE", "utls_chrome")
	clearMaintenanceClients(t)

	accA := &auth.Account{DBID: 4461}
	accB := &auth.Account{DBID: 4462}

	if getCodexMaintenanceClient(accA, "") == getCodexMaintenanceClient(accB, "") {
		t.Fatal("不同账号共享了同一个维护 Client（连接会被跨 token 复用）")
	}
	if getCodexMaintenanceClient(accA, "") == getCodexMaintenanceClient(accA, "http://proxy:8080") {
		t.Fatal("不同代理共享了同一个维护 Client（出口 IP 会串）")
	}
}

// 维护请求与 /responses 主池必须分开：/responses 失败时会摘除整个 Client，
// 不应因一次后台探针失败牵连业务连接，反之亦然。
func TestMaintenanceClientKeyDoesNotCollideWithResponsesPool(t *testing.T) {
	acc := &auth.Account{DBID: 4463}

	maintKey := maintenanceClientKey(acc, "", codexTransportModeUTLSChrome, maintenancePurposeCodex)
	responsesKey := clientPoolKey(acc, "", codexTransportModeUTLSChrome)
	subKey := maintenanceClientKey(acc, "", codexTransportModeUTLSChrome, maintenancePurposeSubscription)

	if maintKey == responsesKey {
		t.Fatalf("维护池键与 /responses 池键冲突: %q", maintKey)
	}
	if maintKey == subKey {
		t.Fatal("订阅同步（浏览器 UA）与 Codex 维护请求（CLI UA）共享了连接")
	}
}

// 订阅端点在 Cloudflare 后面，必须强制 uTLS 指纹，与 CODEX_TRANSPORT_MODE 无关。
func TestSubscriptionMaintenanceClientForcesUTLS(t *testing.T) {
	t.Setenv("CODEX_TRANSPORT_MODE", "standard")
	clearMaintenanceClients(t)

	acc := &auth.Account{DBID: 4464}
	client := getMaintenanceClient(acc, "", maintenancePurposeSubscription, true)

	if _, ok := client.Transport.(*utlsRoundTripper); !ok {
		t.Fatalf("订阅同步 transport = %T, want *utlsRoundTripper（普通指纹会被 Cloudflare 拦截）", client.Transport)
	}
}

// clearMaintenanceClients 清空维护 Client 池，避免用例间互相污染。
func clearMaintenanceClients(t *testing.T) {
	t.Helper()
	clientPool.Range(func(key, value any) bool {
		if k, ok := key.(string); ok && len(k) >= 6 && k[:6] == "maint|" {
			clientPool.Delete(key)
			if entry, ok := value.(*poolEntry); ok {
				releaseEvictedClient(entry.client)
			}
		}
		return true
	})
}
