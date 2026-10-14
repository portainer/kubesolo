package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// health is a set of component probes whose results a test controls.
type health struct {
	mu      sync.Mutex
	failing map[string]error
}

func (h *health) set(component string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failing[component] = err
}

func (h *health) probes() map[string]func(context.Context) error {
	probe := func(name string) func(context.Context) error {
		return func(context.Context) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.failing[name]
		}
	}
	return map[string]func(context.Context) error{"kine": probe("kine"), "apiserver": probe("apiserver")}
}

func newWatcher(t *testing.T) (*Watcher, *health, *time.Time) {
	t.Helper()
	h := &health{failing: map[string]error{}}
	now := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	w := &Watcher{
		Collector: &Collector{Probes: h.probes(), Errors: func() []string { return []string{"kine: database is locked"} }, DataDir: t.TempDir()},
		Store:     NewStore(t.TempDir()),
		Threshold: 5 * time.Minute,
		now:       func() time.Time { return now },
	}
	return w, h, &now
}

func listed(t *testing.T, s *Store) []Summary {
	t.Helper()
	all, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func TestAnOutageIsRecordedOnceItLastsTheThreshold(t *testing.T) {
	w, h, now := newWatcher(t)
	ctx := context.Background()

	h.set("kine", errors.New("dial tcp 127.0.0.1:2379: connection refused"))
	w.check(ctx)
	*now = now.Add(4 * time.Minute)
	w.check(ctx)
	if n := len(listed(t, w.Store)); n != 0 {
		t.Fatalf("recorded %d incidents before the threshold", n)
	}

	// The API server fails too, as it does when kine is down: still one outage.
	h.set("apiserver", errors.New("unexpected status 500"))
	*now = now.Add(time.Minute)
	w.check(ctx)
	*now = now.Add(time.Minute)
	w.check(ctx)

	all := listed(t, w.Store)
	if len(all) != 1 {
		t.Fatalf("recorded %d incidents, want 1", len(all))
	}
	if !strings.Contains(all[0].Trigger, "kine") || !strings.Contains(all[0].Trigger, "apiserver") {
		t.Errorf("trigger %q should name both failing components", all[0].Trigger)
	}
	inc, _ := w.Store.Get(all[0].ID)
	if inc.Kind != KindKubeSolo || !strings.Contains(inc.Snapshot.Components["kine"], "connection refused") || inc.Snapshot.Components["apiserver"] == "ok" {
		t.Errorf("snapshot = %+v", inc.Snapshot)
	}
	if len(inc.Snapshot.RecentErrors) != 1 {
		t.Errorf("recent errors = %q", inc.Snapshot.RecentErrors)
	}
}

func TestANewOutageAfterRecoveryIsRecordedAgain(t *testing.T) {
	w, h, now := newWatcher(t)
	ctx := context.Background()

	for range 2 {
		h.set("kine", errors.New("down"))
		w.check(ctx)
		*now = now.Add(6 * time.Minute)
		w.check(ctx)

		h.set("kine", nil)
		*now = now.Add(time.Minute)
		w.check(ctx)
		*now = now.Add(time.Minute)
	}
	if n := len(listed(t, w.Store)); n != 2 {
		t.Errorf("recorded %d incidents, want 2", n)
	}
}

func TestAShortBlipIsNotAnIncident(t *testing.T) {
	w, h, now := newWatcher(t)
	ctx := context.Background()

	h.set("kine", errors.New("down"))
	w.check(ctx)
	h.set("kine", nil)
	*now = now.Add(time.Minute)
	w.check(ctx)
	h.set("kine", errors.New("down"))
	*now = now.Add(4 * time.Minute)
	w.check(ctx)

	if n := len(listed(t, w.Store)); n != 0 {
		t.Errorf("recorded %d incidents for blips shorter than the threshold", n)
	}
}

func TestUncleanShutdownIsRecordedOnTheNextStart(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "running")

	unclean, err := MarkRunning(marker)
	if err != nil || unclean {
		t.Fatalf("first start: unclean=%v err=%v", unclean, err)
	}
	if err := MarkStopped(marker); err != nil {
		t.Fatal(err)
	}
	if unclean, _ := MarkRunning(marker); unclean {
		t.Error("a clean stop should not count as unclean")
	}
	// No MarkStopped: a crash, a kill or a power loss.
	if unclean, _ := MarkRunning(marker); !unclean {
		t.Error("a run that never stopped cleanly should be reported")
	}
}

// Even a node logging and warning at full rate gives a snapshot that fits its
// share of the model's prompt.
func TestASnapshotFitsItsBudget(t *testing.T) {
	long := strings.Repeat("x", 2000)
	var errs, events []string
	for range 200 {
		errs = append(errs, long)
		events = append(events, long)
	}
	c := &Collector{
		Probes:  map[string]func(context.Context) error{"kine": func(context.Context) error { return errors.New(long) }},
		Errors:  func() []string { return errs },
		Events:  func(context.Context) ([]string, error) { return events, nil },
		DataDir: t.TempDir(),
	}

	raw, err := json.Marshal(c.Collect(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxSnapshotBytes {
		t.Errorf("snapshot is %d bytes, want at most %d", len(raw), maxSnapshotBytes)
	}
}

// A crash loop is one incident: a crash within the window of the previous
// crash, recorded or not, is not recorded again.
func TestACrashLoopIsOneIncident(t *testing.T) {
	store := NewStore(t.TempDir())
	lastCrash := filepath.Join(t.TempDir(), "last-crash")
	start := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)

	crashAt := func(at time.Time) {
		t.Helper()
		if err := RecordUncleanShutdown(store, []string{"apiserver: Error creating leases"}, t.TempDir(), lastCrash, at); err != nil {
			t.Fatal(err)
		}
	}

	// Restarting every 30 seconds for an hour.
	for i := range 120 {
		crashAt(start.Add(time.Duration(i) * 30 * time.Second))
	}
	if n := len(listed(t, store)); n != 1 {
		t.Fatalf("a crash loop recorded %d incidents, want 1", n)
	}

	// After a quiet spell longer than the window, a crash is news again.
	crashAt(start.Add(time.Hour + CrashLoopWindow + time.Minute))
	if n := len(listed(t, store)); n != 2 {
		t.Errorf("a crash after a quiet spell recorded %d incidents in all, want 2", n)
	}
}
