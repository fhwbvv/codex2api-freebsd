package proxy

import (
	"testing"

	"github.com/codex2api/security/promptfilter"
)

// strikeEligibleForDecision 是"这次判定是否累计到人、可导致 NewAPI 自动封号"的
// 单一真相源。封号不可逆,因此本测试逐条锁死安全边界,任何放宽都必须先在这里
// 显式表达并被审视。
func strikeCfg(localSevere, upstreamCYB bool) promptfilter.Config {
	cfg := promptfilter.DefaultConfig()
	cfg.Advanced.Enforcement.LocalSevereStrikeEnabled = localSevere
	cfg.Advanced.Enforcement.CYBStrikeEnabled = upstreamCYB
	return cfg
}

func TestStrikeEligibleForDecision(t *testing.T) {
	localSevere := promptfilter.Decision{
		Action: promptfilter.ActionBlock, ReasonCode: "targeted_operational_intrusion_request",
		StrikeEligible: true, Terminal: true,
	}
	upstreamCYB := promptfilter.Decision{
		Action: promptfilter.ActionBlock, ReasonCode: newAPIUpstreamCyberPolicyReasonCode,
		Terminal: true,
	}

	tests := []struct {
		name    string
		decide  func() promptfilter.Decision
		cfg     promptfilter.Config
		want    bool
		purpose string
	}{
		{
			name:    "local severe violation with switch on accrues strike",
			decide:  func() promptfilter.Decision { return localSevere },
			cfg:     strikeCfg(true, false),
			want:    true,
			purpose: "本地最高置信度严重违规是自动封号的主路径",
		},
		{
			name:    "local severe violation with switch off never accrues",
			decide:  func() promptfilter.Decision { return localSevere },
			cfg:     strikeCfg(false, false),
			want:    false,
			purpose: "运营者可关停本地封号累计",
		},
		{
			name: "local block that is not terminal never accrues",
			decide: func() promptfilter.Decision {
				d := localSevere
				d.Terminal = false
				return d
			},
			cfg:     strikeCfg(true, false),
			want:    false,
			purpose: "非终局命中置信度不足,不得导致封号",
		},
		{
			name: "local block not attributed to current user never accrues",
			decide: func() promptfilter.Decision {
				d := localSevere
				d.StrikeEligible = false // guard pipeline 仅对 current-user+sensitive+terminal 置位
				return d
			},
			cfg:     strikeCfg(true, false),
			want:    false,
			purpose: "工具输出/历史等非当前用户直接输入不得累计到人",
		},
		{
			name: "conversation lock repeat never accrues even if strike flag set",
			decide: func() promptfilter.Decision {
				return promptfilter.Decision{
					Action: promptfilter.ActionBlock, ReasonCode: promptConversationLockedReasonCode,
					StrikeEligible: true, Terminal: true, // 防御性:即使被错误置位
				}
			},
			cfg:     strikeCfg(true, false),
			want:    false,
			purpose: "已锁会话的重复拦截绝不重复累计,否则一次违规会瞬间刷满封号阈值",
		},
		{
			name:    "upstream CYB honours CYBStrikeEnabled on",
			decide:  func() promptfilter.Decision { d := upstreamCYB; d.StrikeEligible = true; return d },
			cfg:     strikeCfg(false, true),
			want:    true,
			purpose: "上游 CYB 是权威封号信号,不受本地开关影响",
		},
		{
			name:    "upstream CYB honours CYBStrikeEnabled off",
			decide:  func() promptfilter.Decision { d := upstreamCYB; d.StrikeEligible = false; return d },
			cfg:     strikeCfg(true, false),
			want:    false,
			purpose: "关闭上游 CYB strike 时,本地开关不得越权替它累计",
		},
		{
			name: "non-block decision never accrues",
			decide: func() promptfilter.Decision {
				d := localSevere
				d.Action = promptfilter.ActionWarn
				return d
			},
			cfg:     strikeCfg(true, true),
			want:    false,
			purpose: "只有拦截才可能累计,警告/放行不得",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := strikeEligibleForDecision(tc.decide(), tc.cfg); got != tc.want {
				t.Fatalf("strikeEligibleForDecision = %t, want %t (%s)", got, tc.want, tc.purpose)
			}
		})
	}
}
