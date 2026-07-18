package auth

import (
	"testing"
	"time"
)

// TestStaleSubscriptionExpiry 验证「付费套餐 + 到期时间已过去」被判为陈旧，
// 其余组合（免费/API/未到期/无到期时间）不受影响。(issue #360)
func TestStaleSubscriptionExpiry(t *testing.T) {
	now := time.Now()
	past := now.Add(-48 * time.Hour)
	future := now.Add(48 * time.Hour)

	cases := []struct {
		name      string
		plan      string
		expiresAt time.Time
		want      bool
	}{
		{"付费套餐+已过去", "plus", past, true},
		{"付费套餐大小写与空白", "  Plus ", past, true},
		{"team+已过去", "team", past, true},
		{"pro+已过去", "pro", past, true},
		{"付费套餐+未到期", "plus", future, false},
		{"free+已过去", "free", past, false},
		{"api+已过去", "api", past, false},
		{"未知套餐+已过去", "", past, false},
		{"付费套餐+零值", "plus", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StaleSubscriptionExpiry(tc.plan, tc.expiresAt, now); got != tc.want {
				t.Fatalf("StaleSubscriptionExpiry(%q, %v) = %v, want %v", tc.plan, tc.expiresAt, got, tc.want)
			}
		})
	}
}

// TestClearStaleSubscriptionExpiresAt 验证清理只作用于陈旧组合：
// 付费套餐的过期时间被清空；free 套餐（真过期）与未到期的付费套餐保持不变。
func TestClearStaleSubscriptionExpiresAt(t *testing.T) {
	past := time.Now().Add(-72 * time.Hour)
	future := time.Now().Add(30 * 24 * time.Hour)

	cases := []struct {
		name        string
		plan        string
		expiresAt   time.Time
		wantCleared bool
	}{
		{"付费套餐陈旧值被清理", "plus", past, true},
		{"free 套餐真过期保留", "free", past, false},
		{"付费套餐未到期保留", "plus", future, false},
		{"无到期时间不动", "plus", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(nil, nil, nil)
			acc := &Account{
				DBID:                  1,
				AccessToken:           "at-test",
				Status:                StatusReady,
				PlanType:              tc.plan,
				SubscriptionExpiresAt: tc.expiresAt,
			}
			store.AddAccount(acc)

			cleared := store.ClearStaleSubscriptionExpiresAt(acc)
			if cleared != tc.wantCleared {
				t.Fatalf("ClearStaleSubscriptionExpiresAt() = %v, want %v", cleared, tc.wantCleared)
			}

			acc.mu.RLock()
			got := acc.SubscriptionExpiresAt
			acc.mu.RUnlock()
			if tc.wantCleared && !got.IsZero() {
				t.Fatalf("SubscriptionExpiresAt = %v, want zero after clear", got)
			}
			if !tc.wantCleared && !got.Equal(tc.expiresAt) {
				t.Fatalf("SubscriptionExpiresAt = %v, want unchanged %v", got, tc.expiresAt)
			}
		})
	}
}

// TestNeedsSubscriptionExpiryProbe 验证订阅到期探针的触发条件与节流：
// 仅付费套餐且到期时间未知/临近/已过时需要，且距上次尝试满足最小间隔。
func TestNeedsSubscriptionExpiryProbe(t *testing.T) {
	now := time.Now()
	interval := 6 * time.Hour

	cases := []struct {
		name      string
		plan      string
		expiresAt time.Time
		probedAt  time.Time
		want      bool
	}{
		{"付费+无到期时间", "plus", time.Time{}, time.Time{}, true},
		{"付费+已过去", "plus", now.Add(-48 * time.Hour), time.Time{}, true},
		{"付费+临近到期(3d)", "plus", now.Add(3 * 24 * time.Hour), time.Time{}, true},
		{"付费+远未到期(30d)", "plus", now.Add(30 * 24 * time.Hour), time.Time{}, false},
		{"free 不探", "free", time.Time{}, time.Time{}, false},
		{"api 不探", "api", time.Time{}, time.Time{}, false},
		{"未知套餐不探", "", time.Time{}, time.Time{}, false},
		{"节流:刚探过", "plus", time.Time{}, now.Add(-time.Hour), false},
		{"节流:间隔已满", "plus", time.Time{}, now.Add(-7 * time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acc := &Account{PlanType: tc.plan, SubscriptionExpiresAt: tc.expiresAt}
			if !tc.probedAt.IsZero() {
				acc.MarkSubscriptionExpiryProbed(tc.probedAt)
			}
			if got := acc.NeedsSubscriptionExpiryProbe(now, interval); got != tc.want {
				t.Fatalf("NeedsSubscriptionExpiryProbe() = %v, want %v", got, tc.want)
			}
		})
	}
}
