package upgrade

import (
	"errors"
	"os"
	"testing"
)

func restartJob(l Layout) Job {
	return Job{ID: NewRunID(), Operation: OpRestart, Request: Request{Version: "v1.2.2"}, DataDir: l.DataDir, From: "v1.2.2"}
}

// Queue records the run and hands its job to the executor: what both the
// API's upgrades and the assistant's restarts go through.
func TestQueueRecordsTheRunAndSpawnsTheExecutor(t *testing.T) {
	l := testLayout(t)
	var spawned string
	accepted, err := Queue(l, restartJob(l), func(jobFile string) error { spawned = jobFile; return nil }, "asked by the assistant")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Operation != OpRestart || spawned == "" {
		t.Errorf("accepted %+v, spawned %q", accepted, spawned)
	}
	if _, err := os.Stat(spawned); err != nil {
		t.Errorf("the job file is missing: %v", err)
	}
	state, err := LoadState(l)
	if err != nil {
		t.Fatal(err)
	}
	if state.Current == nil || state.Current.ID != accepted.ID || state.Current.Phase != PhaseQueued || !state.InFlight() {
		t.Errorf("state = %+v", state.Current)
	}
}

func TestQueueRefusesWhileARunIsInFlight(t *testing.T) {
	l := testLayout(t)
	if _, err := Queue(l, restartJob(l), func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := Queue(l, restartJob(l), func(string) error { return nil }); !errors.Is(err, ErrBusy) {
		t.Errorf("got %v, want ErrBusy", err)
	}
}

func TestQueueUndoesARunThatCannotStart(t *testing.T) {
	l := testLayout(t)
	_, err := Queue(l, restartJob(l), func(string) error { return errors.New("no systemd-run") })
	if err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want the spawn's failure", err)
	}
	if state, _ := LoadState(l); state.InFlight() {
		t.Errorf("a run that never started is left in flight: %+v", state.Current)
	}
	entries, _ := os.ReadDir(l.Dir())
	for _, e := range entries {
		if len(e.Name()) > 4 && e.Name()[:4] == "job-" {
			t.Errorf("job file %s was left", e.Name())
		}
	}
}
