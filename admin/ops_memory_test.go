package admin

import (
	"runtime"
	"strings"
	"testing"
)

func TestParseLinuxProcessRSS(t *testing.T) {
	status := "Name:\tcodex2api\nVmSize:\t1308916 kB\nVmRSS:\t53368 kB\nThreads:\t12\n"
	got, ok := parseLinuxProcessRSS(strings.NewReader(status))
	if !ok {
		t.Fatal("parseLinuxProcessRSS did not find VmRSS")
	}
	const want = uint64(53368 * 1024)
	if got != want {
		t.Fatalf("parseLinuxProcessRSS = %d, want %d", got, want)
	}
}

func TestParseLinuxProcessRSSRejectsMissingOrZeroValue(t *testing.T) {
	for _, status := range []string{
		"Name:\tcodex2api\nVmSize:\t1308916 kB\n",
		"Name:\tcodex2api\nVmRSS:\t0 kB\n",
	} {
		if got, ok := parseLinuxProcessRSS(strings.NewReader(status)); ok || got != 0 {
			t.Fatalf("parseLinuxProcessRSS(%q) = (%d, %t), want (0, false)", status, got, ok)
		}
	}
}

func TestReadProcessMemoryReturnsValue(t *testing.T) {
	got := readProcessMemory()
	if got == 0 {
		t.Fatal("readProcessMemory returned 0")
	}
	if runtime.GOOS == "linux" {
		if rss, ok := parseLinuxProcessRSS(strings.NewReader("VmRSS:\t1 kB\n")); !ok || rss != 1024 {
			t.Fatalf("Linux VmRSS parser sanity check = (%d, %t)", rss, ok)
		}
	}
}
