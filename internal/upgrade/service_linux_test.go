package upgrade

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/cli/detect"
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

// stubRCService puts an rc-service on PATH that records its arguments, one call
// per line, and returns a function reading them back.
func stubRCService(t *testing.T) func() []string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rc-service"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		raw, _ := os.ReadFile(calls)
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if l != "" {
				out = append(out, l)
			}
		}
		return out
	}
}

// OpenRC keeps a service marked started after a stop that gave up, or after the
// process crashed, and then "start" does nothing. With no KubeSolo running the
// mark is stale, so Start clears it first.
func TestOpenRCStartClearsAStaleStartedState(t *testing.T) {
	calls := stubRCService(t)
	svc := &Service{Init: detect.InitOpenRC, binary: filepath.Join(t.TempDir(), "kubesolo")}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if got, want := calls(), []string{"kubesolo zap", "kubesolo start"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rc-service calls = %q, want %q", got, want)
	}
}

func TestOpenRCStartLeavesARunningServiceAlone(t *testing.T) {
	calls := stubRCService(t)
	binary := filepath.Join(t.TempDir(), "kubesolo")
	startFake(t, binary, `trap 'exit 0' TERM; while :; do sleep 0.1; done`)
	svc := &Service{Init: detect.InitOpenRC, binary: binary}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if got, want := calls(), []string{"kubesolo start"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rc-service calls = %q, want %q", got, want)
	}
}

// A process that has exited but not been reaped still holds its PID, and
// OpenRC's start-stop-daemon still counts it as running by its name. Stop has
// to wait for the PID itself to go, not only for the executable link MainPIDs
// reads, which disappears first.
func TestWaitGoneWaitsForTheProcessToBeReaped(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	time.Sleep(200 * time.Millisecond) // exited, not reaped: a zombie

	if waitGone(context.Background(), []int{pid}, 300*time.Millisecond) {
		t.Fatal("waitGone reported an unreaped process gone")
	}
	_ = cmd.Wait()
	if !waitGone(context.Background(), []int{pid}, 2*time.Second) {
		t.Fatal("waitGone did not see the reaped process go")
	}
}
