package auth

import (
	"fmt"
	"testing"

	"github.com/codex2api/database"
)

func newSpreadTestStore(t *testing.T, accounts int) *Store {
	t.Helper()
	store := NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 4, TestConcurrency: 1, TestModel: "gpt-5.4"})
	for i := 1; i <= accounts; i++ {
		store.AddAccount(&Account{DBID: int64(i), AccessToken: fmt.Sprintf("at-%d", i), PlanType: "plus"})
	}
	store.SetSessionAffinitySpread(true)
	return store
}

// 同一亲和键在号池不变时恒命中同一账号(幂等一一绑定,issue #484 诉求)。
func TestFreshAffinitySpreadDeterministic(t *testing.T) {
	store := newSpreadTestStore(t, 3)

	var first int64
	for i := 0; i < 5; i++ {
		acc := store.nextAccountForFreshAffinity("affinity-pro-01", 0, nil, nil)
		if acc == nil {
			t.Fatal("no account selected")
		}
		if first == 0 {
			first = acc.DBID
		} else if acc.DBID != first {
			t.Fatalf("selection drifted: got %d, want stable %d", acc.DBID, first)
		}
		store.Release(acc)
	}
}

// 不同亲和键在候选账号上均匀摊开,不再全部涌向同一账号——这就是 issue #484
// 里 10 个下游键 90% 聚到一个账号的对照场景。
func TestFreshAffinitySpreadDistributes(t *testing.T) {
	store := newSpreadTestStore(t, 3)

	distribution := make(map[int64]int)
	for i := 1; i <= 10; i++ {
		key := fmt.Sprintf("affinity-pro-%02d", i)
		acc := store.nextAccountForFreshAffinity(key, 0, nil, nil)
		if acc == nil {
			t.Fatalf("key %s selected no account", key)
		}
		distribution[acc.DBID]++
		store.Release(acc)
	}
	if len(distribution) < 2 {
		t.Fatalf("10 keys all landed on one account: %v", distribution)
	}
	for id, count := range distribution {
		if count > 7 {
			t.Fatalf("account %d took %d/10 keys, distribution too skewed: %v", id, count, distribution)
		}
	}
}

// 哈希首选被占满时确定性顺延到哈希序下一名;释放后新绑定回到首选。
func TestFreshAffinitySpreadFallsThroughWhenTopBusy(t *testing.T) {
	store := NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 1, TestConcurrency: 1, TestModel: "gpt-5.4"})
	for i := 1; i <= 3; i++ {
		store.AddAccount(&Account{DBID: int64(i), AccessToken: fmt.Sprintf("at-%d", i), PlanType: "plus"})
	}
	store.SetSessionAffinitySpread(true)

	const key = "affinity-fallthrough"
	top := store.nextAccountForFreshAffinity(key, 0, nil, nil)
	if top == nil {
		t.Fatal("no account selected")
	}
	// 首选被占满(MaxConcurrency=1),同键再选必须顺延到别的账号而不是失败。
	second := store.nextAccountForFreshAffinity(key, 0, nil, nil)
	if second == nil {
		t.Fatal("expected fall-through account while top is busy")
	}
	if second.DBID == top.DBID {
		t.Fatalf("fall-through returned the busy account %d", top.DBID)
	}
	store.Release(second)
	store.Release(top)

	again := store.nextAccountForFreshAffinity(key, 0, nil, nil)
	if again == nil || again.DBID != top.DBID {
		t.Fatalf("after release selection = %v, want hash-top %d", again, top.DBID)
	}
	store.Release(again)
}

// 调度优先级严格分层:低优先级账号即使哈希更大也不得越过高优先级层。
func TestFreshAffinitySpreadHonorsSchedulerPriority(t *testing.T) {
	store := newSpreadTestStore(t, 3)
	priority := store.FindByID(2)
	if priority == nil {
		t.Fatal("account 2 missing")
	}
	priority.SetSchedulerPriority(10)

	for i := 1; i <= 6; i++ {
		key := fmt.Sprintf("affinity-prio-%d", i)
		acc := store.nextAccountForFreshAffinity(key, 0, nil, nil)
		if acc == nil {
			t.Fatalf("key %s selected no account", key)
		}
		if acc.DBID != 2 {
			t.Fatalf("key %s bypassed high-priority account, got %d", key, acc.DBID)
		}
		store.Release(acc)
	}
}

// 开关关闭时走原有调度路径;默认值为关。
func TestFreshAffinitySpreadDisabledFallsBack(t *testing.T) {
	store := NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 4, TestConcurrency: 1, TestModel: "gpt-5.4"})
	store.AddAccount(&Account{DBID: 1, AccessToken: "at-1", PlanType: "plus"})
	if store.GetSessionAffinitySpread() {
		t.Fatal("spread should default to disabled")
	}
	acc := store.nextAccountForFreshAffinity("affinity-any", 0, nil, nil)
	if acc == nil {
		t.Fatal("disabled spread should still return an account via the classic scheduler")
	}
	store.Release(acc)
}

// 全链路:经 NextForSessionWithFilter 的新键选号散开,复用路径仍粘住绑定账号。
func TestNextForSessionWithFilterSpreadsFreshKeys(t *testing.T) {
	store := newSpreadTestStore(t, 3)

	bound := make(map[string]int64)
	distribution := make(map[int64]int)
	for i := 1; i <= 9; i++ {
		key := fmt.Sprintf("session-%d", i)
		acc, _ := store.NextForSessionWithFilter(key, 0, nil, nil)
		if acc == nil {
			t.Fatalf("key %s got no account", key)
		}
		store.BindSessionAffinity(key, acc, "")
		bound[key] = acc.DBID
		distribution[acc.DBID]++
		store.Release(acc)
	}
	if len(distribution) < 2 {
		t.Fatalf("fresh keys all clustered: %v", distribution)
	}
	// 复用:已绑定的键仍回到原账号。
	for key, want := range bound {
		acc, _ := store.NextForSessionWithFilter(key, 0, nil, nil)
		if acc == nil || acc.DBID != want {
			t.Fatalf("key %s rebound to %v, want sticky %d", key, acc, want)
		}
		store.Release(acc)
	}
}
