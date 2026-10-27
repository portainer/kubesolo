package assistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeActor carries out actions as a test says.
type fakeActor struct {
	mu      sync.Mutex
	refuse  error // what Check answers
	fail    error // what Run answers
	ran     []string
	release chan struct{} // when set, Run waits on it
	job     string        // when set, Run leaves the action to a job of this ID
	settled map[string]error
}

func (f *fakeActor) Check(_ context.Context, action string, _ Incident) error { return f.refuse }

func (f *fakeActor) Run(_ context.Context, action string, _ Incident) (string, error) {
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, action)
	return f.job, f.fail
}

func (f *fakeActor) Settle(_ context.Context, job string) (bool, error) {
	err, done := f.settled[job]
	return done, err
}

func approved(t *testing.T, s *Store, at time.Time, action string) Incident {
	t.Helper()
	inc := triagedIncident(t, s, at, action, 0.97)
	got, err := s.Review(inc.ID, Review{Verdict: VerdictApproved}, at)
	if err != nil {
		t.Fatal(err)
	}
	return *got
}

func runOf(t *testing.T, s *Store, id string) *ActionRun {
	t.Helper()
	inc, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return inc.ActionRun
}

func TestAnApprovedActionIsCarriedOut(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{}
	r := &Runner{Actor: actor, Store: s, now: func() time.Time { return reviewTime }}
	inc := approved(t, s, reviewTime, "restart_component")

	if err := r.Start(context.Background(), inc.ID); err != nil {
		t.Fatal(err)
	}
	r.Wait()

	run := runOf(t, s, inc.ID)
	if run == nil || run.Status != ActionSucceeded || run.Action != "restart_component" || run.Auto {
		t.Errorf("run = %+v", run)
	}
	if len(actor.ran) != 1 {
		t.Errorf("ran %v", actor.ran)
	}
}

// The preconditions are checked again when the action is carried out: the
// node may have recovered, or changed, since the incident.
func TestAnActionWhosePreconditionsNoLongerHoldIsRefused(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{refuse: errors.New("kubelet is healthy now")}
	r := &Runner{Actor: actor, Store: s, now: func() time.Time { return reviewTime }}
	inc := approved(t, s, reviewTime, "restart_component")

	if err := r.Start(context.Background(), inc.ID); err != nil {
		t.Fatal(err)
	}
	r.Wait()

	run := runOf(t, s, inc.ID)
	if run == nil || run.Status != ActionRefused || run.Reason != "kubelet is healthy now" {
		t.Errorf("run = %+v", run)
	}
	if len(actor.ran) != 0 {
		t.Error("the action ran though its preconditions failed")
	}
}

func TestAFailedActionIsRecorded(t *testing.T) {
	s := NewStore(t.TempDir())
	r := &Runner{Actor: &fakeActor{fail: errors.New("the executor could not start")}, Store: s, now: func() time.Time { return reviewTime }}
	inc := approved(t, s, reviewTime, "prune_images")

	_ = r.Start(context.Background(), inc.ID)
	r.Wait()

	if run := runOf(t, s, inc.ID); run == nil || run.Status != ActionFailed || run.Reason != "the executor could not start" {
		t.Errorf("run = %+v", run)
	}
}

// Only an approved action is carried out, once, and one at a time.
func TestWhatIsNotCarriedOut(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{release: make(chan struct{})}
	r := &Runner{Actor: actor, Store: s, now: func() time.Time { return reviewTime }}

	unreviewed := triagedIncident(t, s, reviewTime, "restart_component", 0.97)
	if err := r.Start(context.Background(), unreviewed.ID); !errors.Is(err, ErrNotApproved) {
		t.Errorf("an unreviewed incident: got %v, want ErrNotApproved", err)
	}

	first := approved(t, s, reviewTime.Add(time.Second), "restart_component")
	second := approved(t, s, reviewTime.Add(2*time.Second), "prune_images")
	if err := r.Start(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), second.ID); !errors.Is(err, ErrActionRunning) {
		t.Errorf("while another runs: got %v, want ErrActionRunning", err)
	}
	if run := runOf(t, s, first.ID); run == nil || run.Status != ActionRunning {
		t.Errorf("first, while running: %+v", run)
	}
	close(actor.release)
	r.Wait()

	if err := r.Start(context.Background(), first.ID); !errors.Is(err, ErrAlreadyCarriedOut) {
		t.Errorf("again: got %v, want ErrAlreadyCarriedOut", err)
	}
}

// A restart ends the process that asked for it: the action is left to a job,
// and settled from the job's outcome once KubeSolo is back.
func TestAnActionLeftToAJobIsSettledLater(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{job: "20261021T030000Z-restart", settled: map[string]error{}}
	r := &Runner{Actor: actor, Store: s, now: func() time.Time { return reviewTime }}
	ok := approved(t, s, reviewTime, "restart_component")
	_ = r.Start(context.Background(), ok.ID)
	r.Wait()

	run := runOf(t, s, ok.ID)
	if run.Status != ActionRunning || run.Job != actor.job {
		t.Fatalf("run, before the job ends = %+v", run)
	}
	r.Settle(context.Background())
	if run := runOf(t, s, ok.ID); run.Status != ActionRunning {
		t.Errorf("settled before the job ended: %+v", run)
	}

	actor.settled[actor.job] = nil
	r.Settle(context.Background())
	if run := runOf(t, s, ok.ID); run.Status != ActionSucceeded || run.End.IsZero() {
		t.Errorf("run, after the job succeeded = %+v", run)
	}
}

func TestAJobThatFailedIsSettledAsFailed(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{job: "j", settled: map[string]error{"j": errors.New("did not become healthy")}}
	r := &Runner{Actor: actor, Store: s, now: func() time.Time { return reviewTime }}
	inc := approved(t, s, reviewTime, "restart_component")
	_ = r.Start(context.Background(), inc.ID)
	r.Wait()

	r.Settle(context.Background())
	if run := runOf(t, s, inc.ID); run.Status != ActionFailed || run.Reason != "did not become healthy" {
		t.Errorf("run = %+v", run)
	}
}

// An action still running with no job to wait for was cut short: KubeSolo
// stopped while carrying it out.
func TestAnActionCutShortIsSettledAsFailed(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := approved(t, s, reviewTime, "prune_images")
	if err := s.update(inc.ID, func(i *Incident) error {
		i.ActionRun = &ActionRun{Action: "prune_images", Status: ActionRunning, Start: reviewTime}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Actor: &fakeActor{}, Store: s, now: func() time.Time { return reviewTime }}

	r.Settle(context.Background())
	if run := runOf(t, s, inc.ID); run.Status != ActionFailed || !strings.Contains(run.Reason, "stopped") {
		t.Errorf("run = %+v", run)
	}
}

// With autoRestart on, a high-rated restart is carried out without approval,
// at most once an hour; nothing else is.
func TestAutomaticRestarts(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{}
	now := reviewTime
	r := &Runner{Actor: actor, Store: s, now: func() time.Time { return now }}

	restart := triagedIncident(t, s, now, "restart_component", 0.97)
	medium := triagedIncident(t, s, now.Add(time.Second), "restart_component", 0.75)
	prune := triagedIncident(t, s, now.Add(2*time.Second), "prune_images", 0.97)
	for _, inc := range []Incident{restart, medium, prune} {
		r.Auto(context.Background(), inc)
		r.Wait()
	}
	if len(actor.ran) != 1 {
		t.Fatalf("ran %v, want only the high-rated restart", actor.ran)
	}
	if run := runOf(t, s, restart.ID); run == nil || !run.Auto || run.Status != ActionSucceeded {
		t.Errorf("run = %+v", run)
	}

	// Another within the hour waits for a person.
	again := triagedIncident(t, s, now.Add(30*time.Minute), "restart_component", 0.99)
	now = now.Add(30 * time.Minute)
	r.Auto(context.Background(), again)
	r.Wait()
	if len(actor.ran) != 1 {
		t.Errorf("a second automatic restart within the hour ran")
	}

	later := triagedIncident(t, s, now.Add(time.Hour), "restart_component", 0.99)
	now = now.Add(time.Hour)
	r.Auto(context.Background(), later)
	r.Wait()
	if len(actor.ran) != 2 {
		t.Errorf("an automatic restart an hour later did not run")
	}
}

// A restart restarts the runner too: the hour is counted from the incidents
// kept, not from memory, or each restart would allow the next at once.
func TestTheAutomaticRestartLimitOutlivesARestart(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{}
	clock := func() time.Time { return reviewTime }
	first := triagedIncident(t, s, reviewTime, "restart_component", 0.97)
	before := &Runner{Actor: actor, Store: s, now: clock}
	before.Auto(context.Background(), first)
	before.Wait()

	restarted := &Runner{Actor: actor, Store: s, now: func() time.Time { return reviewTime.Add(10 * time.Minute) }}
	next := triagedIncident(t, s, reviewTime.Add(10*time.Minute), "restart_component", 0.97)
	restarted.Auto(context.Background(), next)
	restarted.Wait()
	if runOf(t, s, next.ID) != nil {
		t.Error("restarted automatically again within the hour, after a restart")
	}
}

// failed records that inc's action was carried out at and failed.
func failed(t *testing.T, s *Store, inc Incident, at time.Time) {
	t.Helper()
	if err := s.update(inc.ID, func(i *Incident) error {
		i.ActionRun = &ActionRun{Action: i.Triage.Action, Status: ActionFailed, Reason: "KubeSolo restarted, but kubelet: connection refused", Start: at, End: at}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// An action that just failed for the same components would not help: it is
// refused, naming the failure, approved or automatic.
func TestAnActionThatJustFailedIsNotRepeated(t *testing.T) {
	s := NewStore(t.TempDir())
	actor := &fakeActor{}
	now := reviewTime
	r := &Runner{Actor: actor, Store: s, now: func() time.Time { return now }}
	first := approved(t, s, reviewTime.Add(-time.Hour), "restart_component")
	failed(t, s, first, reviewTime.Add(-time.Hour))

	again := approved(t, s, reviewTime, "restart_component")
	if err := r.Start(context.Background(), again.ID); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	run := runOf(t, s, again.ID)
	if run == nil || run.Status != ActionRefused || !strings.Contains(run.Reason, "restart_component failed for kubelet at 2026-10-15 08:00 UTC") {
		t.Errorf("run = %+v", run)
	}

	auto := triagedIncident(t, s, reviewTime.Add(time.Minute), "restart_component", 0.97)
	now = reviewTime.Add(time.Minute)
	r.Auto(context.Background(), auto)
	r.Wait()
	if run := runOf(t, s, auto.ID); run == nil || run.Status != ActionRefused {
		t.Errorf("automatic run = %+v", run)
	}
	if len(actor.ran) != 0 {
		t.Errorf("ran %v", actor.ran)
	}
}

// A failure long ago, or for other components, or of another action, does
// not stop an action.
func TestAnActionIsRepeatedWhenItMightHelp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ago    time.Duration
		action string
		other  string // trigger of the failed incident
	}{
		{"long ago", retryAfter + time.Minute, "restart_component", ""},
		{"another action", time.Hour, "repair_database", ""},
		{"other components", time.Hour, "restart_component", "unhealthy for 5m0s: kine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore(t.TempDir())
			actor := &fakeActor{}
			r := &Runner{Actor: actor, Store: s, now: func() time.Time { return reviewTime }}
			first := approved(t, s, reviewTime.Add(-tc.ago), tc.action)
			if tc.other != "" {
				if err := s.update(first.ID, func(i *Incident) error { i.Trigger = tc.other; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			failed(t, s, first, reviewTime.Add(-tc.ago))

			again := approved(t, s, reviewTime, "restart_component")
			_ = r.Start(context.Background(), again.ID)
			r.Wait()
			if run := runOf(t, s, again.ID); run == nil || run.Status != ActionSucceeded {
				t.Errorf("run = %+v", run)
			}
		})
	}
}
