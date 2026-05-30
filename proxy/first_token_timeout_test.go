package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/codex2api/database"
)

func TestFirstTokenTimeoutGuardCancelsUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	guard := newFirstTokenTimeoutGuard(20*time.Millisecond, cancel)
	defer guard.Stop()

	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first token timeout guard did not cancel upstream context")
	}
	if !guard.TimedOut() {
		t.Fatal("guard TimedOut() = false, want true")
	}
}

func TestFirstTokenTimeoutGuardStopsOnFirstTokenEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := newFirstTokenTimeoutGuard(30*time.Millisecond, cancel)
	defer guard.Stop()

	guard.MarkEvent("response.output_text.delta")

	select {
	case <-ctx.Done():
		t.Fatal("first token timeout guard canceled after first token event")
	case <-time.After(80 * time.Millisecond):
	}
	if guard.TimedOut() {
		t.Fatal("guard TimedOut() = true, want false")
	}
}

func TestNormalizeRuntimeSettingsFirstTokenTimeout(t *testing.T) {
	settings := NormalizeRuntimeSettings(RuntimeSettings{FirstTokenTimeoutSec: -1})
	if settings.FirstTokenTimeoutSec != 0 {
		t.Fatalf("negative first token timeout normalized to %d, want 0", settings.FirstTokenTimeoutSec)
	}

	settings = NormalizeRuntimeSettings(RuntimeSettings{FirstTokenTimeoutSec: 601})
	if settings.FirstTokenTimeoutSec != 600 {
		t.Fatalf("oversized first token timeout normalized to %d, want 600", settings.FirstTokenTimeoutSec)
	}
}

func TestApplyRuntimeSettingsFromSystemFirstTokenTimeout(t *testing.T) {
	defer ApplyRuntimeSettings(DefaultRuntimeSettings())

	settings := ApplyRuntimeSettingsFromSystem(&database.SystemSettings{
		FirstTokenTimeoutSeconds: 42,
	})

	if settings.FirstTokenTimeoutSec != 42 {
		t.Fatalf("FirstTokenTimeoutSec = %d, want 42", settings.FirstTokenTimeoutSec)
	}
	if got := currentFirstTokenTimeout(); got != 42*time.Second {
		t.Fatalf("currentFirstTokenTimeout() = %s, want 42s", got)
	}
}
