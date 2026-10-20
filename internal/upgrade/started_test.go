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
	if err := RecordStarted(l); err != nil {
		t.Fatal(err)
	}
	if pid := StartedPID(l); pid != os.Getpid() {
		t.Errorf("StartedPID = %d, want %d", pid, os.Getpid())
	}
}
