package upgrade

import (
	"fmt"
	"os"
	"time"
)

// Queue records a run for job and starts its executor with spawn, which is
// handed the job file. Taking the lock is what makes the caller the only
// writer: a second caller finds it held, or finds the run recorded and in
// flight, and gets ErrBusy. notes go into the run's log.
func Queue(l Layout, job Job, spawn func(jobFile string) error, notes ...string) (Accepted, error) {
	lock, err := TryLock(l)
	if err != nil {
		return Accepted{}, err
	}
	defer lock.Unlock()

	state, err := LoadState(l)
	if err != nil {
		return Accepted{}, err
	}
	if state.InFlight() {
		c := state.Current
		return Accepted{}, fmt.Errorf("%w (run %s, %s to %s, %s)", ErrBusy, c.ID, c.From, c.To, c.Phase)
	}

	jobFile, err := WriteJob(l, job)
	if err != nil {
		return Accepted{}, err
	}
	now := time.Now().UTC()
	run := &Run{
		ID: job.ID, Operation: job.Operation, From: job.From, To: job.Request.Version,
		Phase: PhaseQueued, Started: now, Updated: now,
	}
	for _, n := range notes {
		run.Log("%s", n)
	}
	previous := state.Current
	state.Current = run
	if err := SaveState(l, state); err != nil {
		_ = os.Remove(jobFile)
		return Accepted{}, err
	}

	if err := spawn(jobFile); err != nil {
		state.Current = previous
		_ = SaveState(l, state)
		_ = os.Remove(jobFile)
		return Accepted{}, fmt.Errorf("could not start the %s: %w", job.Operation, err)
	}
	return Accepted{ID: job.ID, Operation: job.Operation, From: job.From, To: job.Request.Version, StatusPath: "/api/v1/status"}, nil
}
