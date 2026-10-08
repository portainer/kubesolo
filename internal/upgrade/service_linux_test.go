package upgrade

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// startFake runs a shell copied to binary, so that MainPIDs finds it by its
// executable, with script as its program.
func startFake(t *testing.T, binary, script string) *exec.Cmd {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	raw, err := os.ReadFile(sh)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	deadline := time.Now().Add(5 * time.Second)
	for len(MainPIDs(binary)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("fake KubeSolo never showed up in MainPIDs")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cmd
}

// A release up to v1.2.1 exits only on the second SIGTERM. Stop must get it
// out cleanly in seconds, not leave it to a kill.
func TestStopSendsASecondSIGTERM(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kubesolo")
	// Exits 0 on the second TERM; the first only sets a flag.
	startFake(t, binary, `n=0; trap 'n=$((n+1)); [ $n -ge 2 ] && exit 0' TERM; while :; do sleep 0.1; done`)

	svc := &Service{Daemon: true, binary: binary}
	start := time.Now()
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > secondSignalAfter+5*time.Second {
		t.Errorf("stop took %s", took)
	}
	if pids := MainPIDs(binary); len(pids) != 0 {
		t.Errorf("still running: %v", pids)
	}
}

func TestStopOfAPromptProcessIsQuick(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kubesolo")
	startFake(t, binary, `trap 'exit 0' TERM; while :; do sleep 0.1; done`)
	svc := &Service{Daemon: true, binary: binary}
	start := time.Now()
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("stop of a process that exits on the first TERM took %s", took)
	}
}
