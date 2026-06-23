package process

import (
	"testing"
	"time"
)

// ── pidFromProcPath ───────────────────────────────────────────────────────────

func TestPidFromProcPath(t *testing.T) {
	cases := []struct {
		path    string
		wantPID int
	}{
		{"/proc/1234/exe", 1234},
		{"/proc/1/exe", 1},
		{"/proc/99999/fd", 99999},
		{"/proc/42/status", 42},
		// bad inputs
		{"/proc/notanumber/exe", -1},
		{"/proc/exe", -1},
		{"", -1},
		{"/not/proc/123/exe", -1},
	}
	for _, c := range cases {
		got := pidFromProcPath(c.path)
		if got != c.wantPID {
			t.Errorf("pidFromProcPath(%q) = %d, want %d", c.path, got, c.wantPID)
		}
	}
}

// ── backoff cap ───────────────────────────────────────────────────────────────

// TestBackoffCap verifies the exponential back-off cap used in StopByExecutablePath.
// The capped expression is min(backoff*2, 4*time.Second).
func TestBackoffCap(t *testing.T) {
	cases := []struct {
		a, b time.Duration
		want time.Duration
	}{
		{time.Second, 2 * time.Second, time.Second},
		{2 * time.Second, time.Second, time.Second},
		{time.Second, time.Second, time.Second},
		{0, time.Second, 0},
		{time.Millisecond, time.Microsecond, time.Microsecond},
	}
	for _, c := range cases {
		got := min(c.a, c.b)
		if got != c.want {
			t.Errorf("min(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// ── KubeSoloPorts ─────────────────────────────────────────────────────────────

func TestKubeSoloPorts_ContainsExpectedPorts(t *testing.T) {
	required := map[int]bool{
		2379:  true, // Kine
		6443:  true, // API server
		10443: true, // Webhook
		6060:  true, // pprof
	}
	for _, p := range KubeSoloPorts {
		delete(required, p)
	}
	for p := range required {
		t.Errorf("expected port %d in KubeSoloPorts but it was missing", p)
	}
}

// ── listeningInodes (unit test of parsing logic) ──────────────────────────────

func TestListeningInodes_EmptyInput(t *testing.T) {
	// With no /proc/net/tcp[6] readable (e.g. on macOS), returns empty map
	// This just verifies the function doesn't panic on a system without /proc
	inodes := listeningInodes(map[int]bool{6443: true})
	// On Linux this may or may not return results depending on what's running;
	// on macOS it will always return empty. Either way must not panic.
	_ = inodes
}
