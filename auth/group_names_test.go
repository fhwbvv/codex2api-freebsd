package auth

import (
	"testing"

	"github.com/codex2api/database"
)

func TestGroupNameCacheSetResolveDelete(t *testing.T) {
	store := NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 8})

	store.SetGroupName(5, "fast")
	store.SetGroupName(6, "  slow  ") // 应 TrimSpace
	store.SetGroupName(7, "bulk")

	got := store.ResolveGroupNames([]int64{5, 6, 99}) // 99 未知，跳过
	want := []string{"fast", "slow"}
	if len(got) != len(want) {
		t.Fatalf("ResolveGroupNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ResolveGroupNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// 改名生效
	store.SetGroupName(5, "fast-v2")
	if got := store.ResolveGroupNames([]int64{5}); len(got) != 1 || got[0] != "fast-v2" {
		t.Fatalf("改名后 ResolveGroupNames = %v, want [fast-v2]", got)
	}

	// 删除后不再解析出
	store.DeleteGroupName(5)
	if got := store.ResolveGroupNames([]int64{5, 7}); len(got) != 1 || got[0] != "bulk" {
		t.Fatalf("删除后 ResolveGroupNames = %v, want [bulk]", got)
	}

	// 空输入返回 nil
	if got := store.ResolveGroupNames(nil); got != nil {
		t.Fatalf("空输入应返回 nil, got %v", got)
	}
}
