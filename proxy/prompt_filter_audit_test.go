package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/codex2api/database"
)

func waitPromptFilterAuditIdle(t testing.TB, db *database.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !db.WaitPromptFilterAuditIdle(ctx) {
		t.Fatal("timed out waiting for prompt filter audit queue")
	}
}
