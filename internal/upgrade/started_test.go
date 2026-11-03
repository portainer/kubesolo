package upgrade

import (
	"os"
	"testing"
)

func TestStartedPID(t *testing.T) {
	l := testLayout(t)

	if pid := StartedPID(l); pid != 0 {
		t.Fatalf("before KubeSolo has started: %d", pid)
	}
	raw := []byte("portainer:\n  edgeID: edge-id\n")
	if err := RecordStarted(l, raw); err != nil {
		t.Fatal(err)
	}
	if pid := StartedPID(l); pid != os.Getpid() {
		t.Errorf("StartedPID = %d, want %d", pid, os.Getpid())
	}
	if got, err := os.ReadFile(l.StartedConfig()); err != nil || string(got) != string(raw) {
		t.Errorf("started config = %q, %v", got, err)
	}
	if fi, err := os.Stat(l.StartedConfig()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("started config mode %v, %v", fi.Mode(), err)
	}
}
