package proxy

import "testing"

func TestToolInputCorrectorStats(t *testing.T) {
	corrector := NewClaudeToolCorrector()

	if stats := corrector.GetStats(); stats.TotalCorrected != 0 || len(stats.CorrectionsByTool) != 0 || len(stats.CorrectionsByReason) != 0 {
		t.Fatalf("initial stats = %+v, want empty", stats)
	}

	if _, corrected := corrector.CorrectToolInputJSON("Read", `{"file_path":"/etc/hosts","pages":""}`); !corrected {
		t.Fatalf("expected Read pages correction")
	}
	if _, corrected := corrector.CorrectToolInputJSON("EnterWorktree", `{"name":"","path":"F:\\Github\\codex2api\\.claude\\worktrees\\existing"}`); !corrected {
		t.Fatalf("expected EnterWorktree name correction")
	}
	if _, corrected := corrector.CorrectToolInputJSON("Write", `{"content":""}`); corrected {
		t.Fatalf("Write empty content must not be corrected")
	}

	stats := corrector.GetStats()
	if stats.TotalCorrected != 2 {
		t.Fatalf("TotalCorrected = %d, want 2", stats.TotalCorrected)
	}
	if stats.CorrectionsByTool["Read"] != 1 {
		t.Fatalf("Read tool count = %d, want 1", stats.CorrectionsByTool["Read"])
	}
	if stats.CorrectionsByTool["EnterWorktree"] != 1 {
		t.Fatalf("EnterWorktree tool count = %d, want 1", stats.CorrectionsByTool["EnterWorktree"])
	}
	if stats.CorrectionsByReason["Read.pages:empty"] != 1 {
		t.Fatalf("Read.pages:empty count = %d, want 1", stats.CorrectionsByReason["Read.pages:empty"])
	}
	if stats.CorrectionsByReason["EnterWorktree.name:empty-with-path"] != 1 {
		t.Fatalf("EnterWorktree.name count = %d, want 1", stats.CorrectionsByReason["EnterWorktree.name:empty-with-path"])
	}

	corrector.ResetStats()
	stats = corrector.GetStats()
	if stats.TotalCorrected != 0 || len(stats.CorrectionsByTool) != 0 || len(stats.CorrectionsByReason) != 0 {
		t.Fatalf("stats after reset = %+v, want empty", stats)
	}
}
