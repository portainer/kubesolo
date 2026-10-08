package upgrade

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Operation is what a run does.
type Operation string

const (
	OpUpgrade  Operation = "upgrade"
	OpRollback Operation = "rollback"
)

// Phase is where a run has got to. The phases before PhaseStopping change
// nothing on the host; a failure in one of them is reported as ResultAborted.
type Phase string

const (
	PhaseQueued      Phase = "queued"
	PhasePreflight   Phase = "preflight"
	PhaseBackup      Phase = "backup"
	PhaseDryRun      Phase = "dry-run"
	PhaseStopping    Phase = "stopping"
	PhaseReplacing   Phase = "replacing"
	PhaseStarting    Phase = "starting"
	PhaseVerifying   Phase = "verifying"
	PhaseRollingBack Phase = "rolling-back"
	PhaseDone        Phase = "done"
)

// Result is how a run ended.
type Result string

const (
	// ResultSucceeded: the new version is running and passed the health gate.
	ResultSucceeded Result = "succeeded"

	// ResultAborted: the run failed before anything on the host was changed.
	// KubeSolo kept running throughout.
	ResultAborted Result = "aborted"

	// ResultRolledBack: the new version failed the health gate and the previous
	// binary, datastore and configuration were restored and are running.
	ResultRolledBack Result = "rolled-back"

	// ResultFailed: the run changed the host and could not bring it back to a
	// healthy state. It needs attention.
	ResultFailed Result = "failed"
)

// Run is one upgrade or rollback.
type Run struct {
	ID        string    `json:"id"`
	Operation Operation `json:"operation"`

	// From is the version that was running when the run began; To is the one
	// it is moving to.
	From string `json:"from"`
	To   string `json:"to"`

	Phase    Phase     `json:"phase"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	Finished time.Time `json:"finished,omitzero"`

	Result Result `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`

	// HostChanged becomes true at the first destructive step. Until then a
	// failure leaves the host exactly as it was.
	HostChanged bool `json:"hostChanged"`

	// PID is the executor's process. Zero until it has started.
	PID int `json:"pid,omitempty"`

	// Messages is a short log of what the run did, oldest first.
	Messages []string `json:"messages,omitempty"`
}

// State is the upgrade record kept on disk. It is read after the restart an
// upgrade causes, which is the point: a KubeSolo that has just been rolled back
// can still say what happened to it.
type State struct {
	// Current is the run in flight, if any.
	Current *Run `json:"current,omitempty"`

	// Last is the most recent run to finish.
	Last *Run `json:"last,omitempty"`
}

// maxMessages bounds Run.Messages so the state file stays small.
const maxMessages = 50

// Log appends a message to the run.
func (r *Run) Log(format string, args ...any) {
	msg := time.Now().UTC().Format(time.RFC3339) + " " + fmt.Sprintf(format, args...)
	r.Messages = append(r.Messages, msg)
	if len(r.Messages) > maxMessages {
		r.Messages = r.Messages[len(r.Messages)-maxMessages:]
	}
	r.Updated = time.Now().UTC()
}

// NewRunID returns a short random identifier for a run.
func NewRunID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LoadState reads the state file. A missing file is an empty state.
func LoadState(l Layout) (*State, error) {
	raw, err := os.ReadFile(l.StateFile())
	if errors.Is(err, fs.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("upgrade state %s is unreadable: %w", l.StateFile(), err)
	}
	return &s, nil
}

// SaveState writes the state file atomically: a crash mid-write leaves the
// previous state, never a truncated one.
func SaveState(l Layout, s *State) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(l.StateFile(), raw, 0o600)
}

// WriteFileAtomic writes data to a temporary file beside path, syncs it, and
// renames it over path.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// queuedGrace is how long a run that has been accepted but whose executor has
// not yet recorded its PID still counts as in flight.
const queuedGrace = 2 * time.Minute

// InFlight reports whether s.Current is a run that is still going: its
// executor is alive, or it was queued moments ago and the executor has not
// started yet. A run whose executor died is not in flight; it is stale.
func (s *State) InFlight() bool {
	if s.Current == nil {
		return false
	}
	if s.Current.PID > 0 {
		return processAlive(s.Current.PID)
	}
	return time.Since(s.Current.Started) < queuedGrace
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// CloseInterrupted finishes s.Current, a run whose executor stopped before it
// did, now that running is the version KubeSolo is running and healthy. It
// returns false when there is no such run.
func (s *State) CloseInterrupted(running string, guardEvent string) bool {
	run := s.Current
	if run == nil || s.InFlight() {
		return false
	}
	// The guard log keeps every restore. Only one made during this run is it.
	if ts, _, ok := strings.Cut(guardEvent, " "); !ok {
		guardEvent = ""
	} else if at, err := time.Parse(time.RFC3339, ts); err != nil || at.Before(run.Started.Truncate(time.Second)) {
		guardEvent = ""
	}
	switch {
	case run.To == running && run.Operation == OpUpgrade:
		run.Result, run.Error = ResultSucceeded, ""
		run.Log("the executor stopped before it finished, but %s came up healthy", running)
	case run.To == running:
		run.Result, run.Error = ResultSucceeded, ""
		run.Log("the executor stopped before it finished, but the rollback to %s came up healthy", running)
	case run.From == running && guardEvent != "":
		run.Result = ResultRolledBack
		run.Error = "the executor stopped before it finished; the boot guard restored the previous version: " + guardEvent
		run.Log("%s", run.Error)
	case run.From == running && !run.HostChanged:
		run.Result = ResultAborted
		run.Error = "the executor stopped before it changed anything"
		run.Log("%s", run.Error)
	default:
		run.Result = ResultFailed
		run.Error = fmt.Sprintf("the executor stopped before it finished; %s is running", running)
		run.Log("%s", run.Error)
	}
	run.Phase = PhaseDone
	run.Finished = time.Now().UTC()
	s.Last, s.Current = run, nil
	return true
}

// ErrBusy is returned when another upgrade or rollback holds the lock.
var ErrBusy = errors.New("another upgrade or rollback is in progress")

// Lock is the exclusive lock on the upgrade directory. Whoever holds it is the
// only writer of the binary, the datastore backup and the state file — the API,
// kubesoloctl and the executor all take it, so a human running kubesoloctl
// cannot start an upgrade into one Portainer triggered.
type Lock struct {
	f *os.File
}

// TryLock takes the lock without waiting. It returns ErrBusy if it is held.
func TryLock(l Layout) (*Lock, error) {
	return lock(l, syscall.LOCK_EX|syscall.LOCK_NB)
}

// WaitLock takes the lock, waiting up to timeout for it.
func WaitLock(l Layout, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		lk, err := TryLock(l)
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			return lk, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func lock(l Layout, how int) (*Lock, error) {
	if err := os.MkdirAll(l.Dir(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(l.LockFile(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock.
func (lk *Lock) Unlock() {
	if lk == nil || lk.f == nil {
		return
	}
	_ = syscall.Flock(int(lk.f.Fd()), syscall.LOCK_UN)
	_ = lk.f.Close()
	lk.f = nil
}
