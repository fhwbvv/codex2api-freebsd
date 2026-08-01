package proxy

import "testing"

func TestDefaultRuntimeSettingsCodexMinCLIVersion(t *testing.T) {
	if got := DefaultRuntimeSettings().CodexMinCLIVersion; got != "0.144.1" {
		t.Fatalf("CodexMinCLIVersion = %q, want 0.144.1", got)
	}
}
