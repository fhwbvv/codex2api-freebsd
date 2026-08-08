package auth

import "testing"

func TestResolveProxyForAccountPrefersAccountProxy(t *testing.T) {
	store := &Store{
		globalProxy:      "http://global-proxy:8080",
		proxyPoolEnabled: true,
		proxyPool:        []string{"http://pool-1:8080"},
	}
	account := &Account{
		DBID:     7,
		ProxyURL: " http://account-proxy:8080 ",
	}

	got := store.ResolveProxyForAccount(account)
	want := "http://account-proxy:8080"
	if got != want {
		t.Fatalf("ResolveProxyForAccount() = %q, want %q", got, want)
	}
}

func TestResolveProxyForAccountUsesStickyProxyPool(t *testing.T) {
	store := &Store{
		globalProxy:      "http://global-proxy:8080",
		proxyPoolEnabled: true,
		proxyPool: []string{
			"http://pool-1:8080",
			"http://pool-2:8080",
			"http://pool-3:8080",
		},
	}

	cases := []struct {
		id   int64
		want string
	}{
		{id: 1, want: "http://pool-1:8080"},
		{id: 2, want: "http://pool-2:8080"},
		{id: 3, want: "http://pool-3:8080"},
		{id: 4, want: "http://pool-1:8080"},
	}
	for _, tc := range cases {
		account := &Account{DBID: tc.id}
		first := store.ResolveProxyForAccount(account)
		second := store.ResolveProxyForAccount(account)
		if first != tc.want || second != tc.want {
			t.Fatalf("account %d proxy = %q/%q, want sticky %q", tc.id, first, second, tc.want)
		}
	}
}

func TestResolveProxyForAccountFallsBackToGlobalProxy(t *testing.T) {
	store := &Store{
		globalProxy:      " http://global-proxy:8080 ",
		proxyPoolEnabled: false,
		proxyPool:        []string{"http://pool-1:8080"},
	}

	got := store.ResolveProxyForAccount(&Account{DBID: 1})
	want := "http://global-proxy:8080"
	if got != want {
		t.Fatalf("ResolveProxyForAccount() = %q, want %q", got, want)
	}
}

func TestResolveProxyForAccountSkipsBlankPoolEntries(t *testing.T) {
	store := &Store{
		globalProxy:      "http://global-proxy:8080",
		proxyPoolEnabled: true,
		proxyPool:        []string{"", " http://pool-2:8080 "},
	}

	got := store.ResolveProxyForAccount(&Account{DBID: 1})
	want := "http://pool-2:8080"
	if got != want {
		t.Fatalf("ResolveProxyForAccount() = %q, want %q", got, want)
	}
}

// 组代理优先级:账号代理 > 组代理 > 粘性代理池 > 全局(issue #479)。
func TestResolveProxyForAccountGroupProxyBeatsPoolAndGlobal(t *testing.T) {
	store := &Store{
		globalProxy:      "http://global-proxy:8080",
		proxyPoolEnabled: true,
		proxyPool:        []string{"http://pool-1:8080"},
	}
	store.SetGroupProxyURLs(5, []string{" http://group-proxy:8080 "})

	got := store.ResolveProxyForAccount(&Account{DBID: 1, GroupIDs: []int64{5}})
	if want := "http://group-proxy:8080"; got != want {
		t.Fatalf("ResolveProxyForAccount() = %q, want group proxy %q", got, want)
	}

	// 账号自身代理仍然最高优先。
	got = store.ResolveProxyForAccount(&Account{DBID: 1, GroupIDs: []int64{5}, ProxyURL: "http://account-proxy:8080"})
	if want := "http://account-proxy:8080"; got != want {
		t.Fatalf("ResolveProxyForAccount() = %q, want account proxy %q", got, want)
	}
}

// 组内多条代理按账号 ID 粘性:同账号稳定命中同一条,不随调用漂移。
func TestResolveProxyForAccountGroupProxySticky(t *testing.T) {
	store := &Store{}
	store.SetGroupProxyURLs(5, []string{
		"http://g-1:8080",
		"http://g-2:8080",
		"http://g-3:8080",
	})

	seen := make(map[int64]string)
	for _, id := range []int64{1, 2, 3, 4, 5, 6} {
		account := &Account{DBID: id, GroupIDs: []int64{5}}
		first := store.ResolveProxyForAccount(account)
		second := store.ResolveProxyForAccount(account)
		if first == "" || first != second {
			t.Fatalf("account %d proxy not sticky: %q vs %q", id, first, second)
		}
		seen[id] = first
	}
	// 粘性哈希应把不同账号分散到多条代理上,而不是全挤在一条。
	distinct := make(map[string]struct{})
	for _, proxy := range seen {
		distinct[proxy] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Fatalf("expected accounts spread across proxies, all got %v", seen)
	}
}

// 多组归属:按 GroupIDs 顺序取第一个配置了代理的组。
func TestResolveProxyForAccountMultiGroupFirstConfiguredWins(t *testing.T) {
	store := &Store{}
	store.SetGroupProxyURLs(9, []string{"http://group9:8080"})

	// 组 3 未配置代理,跳过后命中组 9。
	got := store.ResolveProxyForAccount(&Account{DBID: 1, GroupIDs: []int64{3, 9}})
	if want := "http://group9:8080"; got != want {
		t.Fatalf("ResolveProxyForAccount() = %q, want %q", got, want)
	}
}

// 热更新:改组代理即时生效,清空后回退到全局链。
func TestSetGroupProxyURLsHotUpdate(t *testing.T) {
	store := &Store{globalProxy: "http://global-proxy:8080"}
	account := &Account{DBID: 1, GroupIDs: []int64{5}}

	store.SetGroupProxyURLs(5, []string{"http://old:8080"})
	if got := store.ResolveProxyForAccount(account); got != "http://old:8080" {
		t.Fatalf("before update proxy = %q", got)
	}
	store.SetGroupProxyURLs(5, []string{"http://new:8080"})
	if got := store.ResolveProxyForAccount(account); got != "http://new:8080" {
		t.Fatalf("after update proxy = %q, want new", got)
	}
	// 全空白列表等价于清除。
	store.SetGroupProxyURLs(5, []string{" ", ""})
	if got := store.ResolveProxyForAccount(account); got != "http://global-proxy:8080" {
		t.Fatalf("after clear proxy = %q, want global fallback", got)
	}
}

// 粘性会话校验:组代理变更后,粘住旧代理的绑定要判失效触发重绑。
func TestAffinityProxyStillValidTracksGroupProxy(t *testing.T) {
	store := &Store{}
	account := &Account{DBID: 42, AccessToken: "at", GroupIDs: []int64{5}}
	store.accounts = []*Account{account}
	store.rebuildAccountIndex()
	store.SetGroupProxyURLs(5, []string{"http://group-a:8080"})

	if !store.affinityProxyStillValid(42, "http://group-a:8080") {
		t.Fatal("current group proxy should be valid")
	}
	store.SetGroupProxyURLs(5, []string{"http://group-b:8080"})
	if store.affinityProxyStillValid(42, "http://group-a:8080") {
		t.Fatal("stale group proxy should be invalid after update")
	}
	if !store.affinityProxyStillValid(42, "http://group-b:8080") {
		t.Fatal("new group proxy should be valid")
	}
}
