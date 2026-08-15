package auth

import (
	"testing"
	"time"
)

// TestTransportFailureNeedsStreakBeforeTierDrop 验证孤立的传输层断流不再削账号
// 并发,连续失败仍会降档(issue #491)。
func TestTransportFailureNeedsStreakBeforeTierDrop(t *testing.T) {
	s := &Store{}
	acc := &Account{HealthTier: HealthTierHealthy}

	// 阈值之前的每一次断流都不该把健康账号打下 Healthy。
	for i := 1; i < transportFailureTierDropStreak; i++ {
		s.ReportRequestFailure(acc, transportFailureKind, 10*time.Millisecond)
		if acc.HealthTier != HealthTierHealthy {
			t.Fatalf("isolated transport failure #%d dropped tier to %s", i, acc.HealthTier)
		}
	}
	// 达到连击阈值:豁免失效,账号降档,调度随之把流量挪走。
	s.ReportRequestFailure(acc, transportFailureKind, 10*time.Millisecond)
	if acc.HealthTier != HealthTierWarm {
		t.Fatalf("streak of %d transport failures should drop to warm, got %s", transportFailureTierDropStreak, acc.HealthTier)
	}
	if !acc.LastFailureAt.After(acc.LastSuccessAt) || acc.LastFailureKind != transportFailureKind {
		t.Fatalf("failure attribution not recorded: kind=%q", acc.LastFailureKind)
	}

	// 成功清零连击,豁免重新生效(此处只断言豁免判据本身:该账号此刻的
	// 成功率已被前面连续失败拉低,最终档位由分数决定,不适合再断言 tier)。
	s.ReportRequestSuccess(acc, 10*time.Millisecond)
	if acc.FailureStreak != 0 {
		t.Fatalf("success must reset failure streak, got %d", acc.FailureStreak)
	}
	s.ReportRequestFailure(acc, transportFailureKind, 10*time.Millisecond)
	if !acc.isolatedTransportFailureLocked() {
		t.Fatal("post-success single transport failure must be treated as isolated again")
	}
}

// TestNonTransportFailureStillDropsImmediately 验证豁免只针对传输层:
// server/timeout 首次即降档,unauthorized 仍然封禁。
func TestNonTransportFailureStillDropsImmediately(t *testing.T) {
	s := &Store{}
	for _, kind := range []string{"server", "timeout"} {
		acc := &Account{HealthTier: HealthTierHealthy}
		s.ReportRequestFailure(acc, kind, 10*time.Millisecond)
		if acc.HealthTier != HealthTierWarm {
			t.Fatalf("%s failure must drop tier on first hit, got %s", kind, acc.HealthTier)
		}
		if acc.isolatedTransportFailureLocked() {
			t.Fatalf("%s failure must not qualify for the transport exemption", kind)
		}
	}

	banned := &Account{HealthTier: HealthTierHealthy}
	s.ReportRequestFailure(banned, "unauthorized", 10*time.Millisecond)
	if banned.HealthTier != HealthTierBanned {
		t.Fatalf("unauthorized must still ban, got %s", banned.HealthTier)
	}
}
