package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
	appsv1 "k8s.io/api/apps/v1"
)

// fakeAgent stands in for the cluster: snapshot returns before, a running
// agent when it is nil, and verify returns the next error from verdicts, then
// nil.
type fakeAgent struct {
	before   *agentState
	verdicts []error
	verified int

	// onSnapshot, when set, runs as the agent is read before the stop.
	onSnapshot func()
}

func (a *fakeAgent) snapshot(context.Context) (*agentState, error) {
	if a.onSnapshot != nil {
		a.onSnapshot()
	}
	if a.before != nil {
		return a.before, nil
	}
	return &agentState{deployment: &appsv1.Deployment{}, pod: "p1", container: "c1", ready: true}, nil
}

func (a *fakeAgent) verify(context.Context, *agentState, time.Time) error {
	a.verified++
	if len(a.verdicts) == 0 {
		return nil
	}
	v := a.verdicts[0]
	a.verdicts = a.verdicts[1:]
	return v
}

func gates(outcomes ...gateOutcome) func(string, time.Duration, bool) gateOutcome {
	return func(string, time.Duration, bool) gateOutcome {
		if len(outcomes) == 0 {
			return gateOutcome{gateHealthy, "healthy"}
		}
		o := outcomes[0]
		outcomes = outcomes[1:]
		return o
	}
}

func host(gate func(string, time.Duration, bool) gateOutcome, agent agentCheck) restartHost {
	return restartHost{
		stop:  func(context.Context) error { return nil },
		start: func() error { return nil },
		gate:  gate,
		agent: agent,
	}
}

var (
	goodConfig = []byte("network:\n  mtu: 1500\n")
	badConfig  = []byte("network:\n  mtu: 9000\n")
	unhealthy  = gateOutcome{gateUnhealthy, "not healthy after 10m0s: node not Ready"}
)

// newRestartExecutor is an executor partway through a restart run, with the
// configuration file holding badConfig and good the known-good configuration.
func newRestartExecutor(t *testing.T, good []byte) *executor {
	t.Helper()
	dir := t.TempDir()
	job := upgrade.Job{
		ID: "r1", Operation: upgrade.OpRestart, Request: upgrade.Request{Version: "v1.2.2"},
		DataDir: dir, ConfigFile: filepath.Join(dir, "config.yaml"), From: "v1.2.2",
	}
	if err := os.WriteFile(job.ConfigFile, badConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	e := &executor{job: job, l: job.Layout(), ctx: context.Background(), state: &upgrade.State{}, from: "v1.2.2", good: good}
	e.claimRun()
	return e
}

// finished runs the restart's host-changing part and returns the run as
// /api/v1/status reads it back.
func finished(t *testing.T, e *executor, h restartHost) *upgrade.Run {
	t.Helper()
	result, err := e.cycle(h)
	e.finish(result, err)
	st := upgrade.ReadUpgradeStatus(e.l, "v1.2.2")
	if st.InFlight || st.Current != nil || st.Last == nil {
		t.Fatalf("status = %+v", st)
	}
	if st.Last.Operation != upgrade.OpRestart || st.Last.Phase != upgrade.PhaseDone || !st.Last.HostChanged || st.Last.Finished.IsZero() {
		t.Errorf("last = %+v", st.Last)
	}
	return st.Last
}

func configFile(t *testing.T, e *executor) string {
	t.Helper()
	raw, err := os.ReadFile(e.l.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Without a known-good configuration there is nothing to go back to: whatever
// the gate or the agent check decides is reported as it is.
func TestRestartOutcomeWithoutKnownGood(t *testing.T) {
	cases := []struct {
		name  string
		host  restartHost
		want  upgrade.Result
		error string
	}{
		{"healthy", host(gates(), &fakeAgent{}), upgrade.ResultSucceeded, ""},
		{"unhealthy", host(gates(unhealthy), &fakeAgent{}), upgrade.ResultFailed, "did not become healthy after the restart: not healthy after 10m0s: node not Ready"},
		{"crash loop", host(gates(gateOutcome{gateUnhealthy, "KubeSolo exited"}), &fakeAgent{}), upgrade.ResultFailed, "KubeSolo exited"},
		{"agent disrupted", host(gates(), &fakeAgent{verdicts: []error{errors.New("the Portainer agent's container was restarted, so the restart disrupted it")}}), upgrade.ResultFailed, "container was restarted"},
		{"stop fails", restartHost{stop: func(context.Context) error { return errors.New("KubeSolo did not stop") }, start: func() error { return nil }, gate: gates(), agent: &fakeAgent{}}, upgrade.ResultFailed, "stop KubeSolo: KubeSolo did not stop"},
		{"start fails", restartHost{stop: func(context.Context) error { return nil }, start: func() error { return errors.New("systemctl start kubesolo: exit status 1") }, gate: gates(), agent: &fakeAgent{}}, upgrade.ResultFailed, "start KubeSolo v1.2.2: systemctl start kubesolo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRestartExecutor(t, nil)
			last := finished(t, e, c.host)
			if last.Result != c.want {
				t.Errorf("result = %s, want %s", last.Result, c.want)
			}
			if !strings.Contains(last.Error, c.error) || (c.error == "") != (last.Error == "") {
				t.Errorf("error = %q, want it to contain %q", last.Error, c.error)
			}
			if configFile(t, e) != string(badConfig) {
				t.Error("the configuration file changed without a known-good one to revert to")
			}
		})
	}
}

// A restart that leaves KubeSolo or the agent down goes back to the known-good
// configuration, keeps the one that failed, and says why.
func TestFailedRestartRevertsToKnownGood(t *testing.T) {
	cases := []struct {
		name  string
		gate  func(string, time.Duration, bool) gateOutcome
		agent *fakeAgent
		cause string

		// checks is how many times the agent is checked: after each start
		// that KubeSolo came back healthy from.
		checks int
	}{
		{"KubeSolo unhealthy", gates(unhealthy, gateOutcome{gateHealthy, "healthy"}), &fakeAgent{}, "did not become healthy after the restart", 1},
		{"agent not running", gates(), &fakeAgent{verdicts: []error{errors.New("the Portainer agent is not running: no ready pod runs portainer/agent:bad")}}, "no ready pod runs portainer/agent:bad", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRestartExecutor(t, goodConfig)
			last := finished(t, e, host(c.gate, c.agent))

			if last.Result != upgrade.ResultRolledBack || !strings.Contains(last.Error, c.cause) {
				t.Errorf("result %s, error %q; want rolled-back because %q", last.Result, last.Error, c.cause)
			}
			if configFile(t, e) != string(goodConfig) {
				t.Errorf("configuration file after the revert = %q", configFile(t, e))
			}
			if kept, err := os.ReadFile(e.l.FailedConfig()); err != nil || string(kept) != string(badConfig) {
				t.Errorf("the configuration that failed was not kept: %q, %v", kept, err)
			}
			if fi, err := os.Stat(e.l.ConfigFile); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("configuration file mode %v, %v", fi.Mode(), err)
			}
			if c.agent.verified != c.checks {
				t.Errorf("the agent was checked %d times, want %d", c.agent.verified, c.checks)
			}
			if msgs := strings.Join(last.Messages, "\n"); !strings.Contains(msgs, "reverting to the known-good configuration") || !strings.Contains(msgs, "healthy on the known-good configuration again") {
				t.Errorf("the run log does not tell the revert:\n%s", msgs)
			}
		})
	}
}

// If KubeSolo does not come back on the known-good configuration either, the
// host needs attention, and the run says what failed both times.
func TestRevertThatFailsIsReportedAsFailed(t *testing.T) {
	e := newRestartExecutor(t, goodConfig)
	last := finished(t, e, host(gates(unhealthy, gateOutcome{gateUnhealthy, "KubeSolo exited"}), &fakeAgent{}))
	if last.Result != upgrade.ResultFailed || !strings.Contains(last.Error, "node not Ready") || !strings.Contains(last.Error, "did not come back on the known-good configuration either") {
		t.Errorf("result %s, error %q", last.Result, last.Error)
	}
	if configFile(t, e) != string(goodConfig) {
		t.Error("the known-good configuration is not the one left in place")
	}
}

// A stop that fails leaves KubeSolo on the configuration it was running, so
// there is nothing to revert.
func TestStopFailureIsNotReverted(t *testing.T) {
	e := newRestartExecutor(t, goodConfig)
	h := host(gates(), &fakeAgent{})
	h.stop = func(context.Context) error { return errors.New("KubeSolo did not stop") }
	last := finished(t, e, h)
	if last.Result != upgrade.ResultFailed || configFile(t, e) != string(badConfig) {
		t.Errorf("result %s, configuration %q", last.Result, configFile(t, e))
	}
	if _, err := os.Stat(e.l.FailedConfig()); !os.IsNotExist(err) {
		t.Error("a failed copy was kept for a restart that was not reverted")
	}
}

// Interrupted mid-verification, the restart stays open for the KubeSolo that
// comes back to close, as an upgrade does, and nothing is reverted.
func TestInterruptedRestartIsLeftForKubeSoloToClose(t *testing.T) {
	for _, h := range []restartHost{
		host(gates(gateOutcome{gateInterrupted, "interrupted"}), &fakeAgent{}),
		host(gates(), &fakeAgent{verdicts: []error{errInterrupted}}),
	} {
		e := newRestartExecutor(t, goodConfig)
		_, err := e.cycle(h)
		if !errors.Is(err, errLeftPending) {
			t.Fatalf("err = %v, want errLeftPending", err)
		}
		if e.state.Current == nil || e.state.Current.Phase != upgrade.PhaseVerifying || e.state.Last != nil {
			t.Errorf("state = current %+v, last %+v", e.state.Current, e.state.Last)
		}
		if configFile(t, e) != string(badConfig) {
			t.Error("an interrupted restart was reverted")
		}
	}
}

// An executor signalled while verifying the revert leaves the run, still
// rolling-back, for KubeSolo to close.
func TestInterruptedRevertIsLeftForKubeSoloToClose(t *testing.T) {
	e := newRestartExecutor(t, goodConfig)
	_, err := e.cycle(host(gates(unhealthy, gateOutcome{gateInterrupted, "interrupted"}), &fakeAgent{}))
	if !errors.Is(err, errLeftPending) {
		t.Fatalf("err = %v, want errLeftPending", err)
	}
	if e.state.Current == nil || e.state.Current.Phase != upgrade.PhaseRollingBack {
		t.Errorf("current = %+v, want it still rolling-back", e.state.Current)
	}
	if configFile(t, e) != string(goodConfig) {
		t.Error("the known-good configuration is not in place")
	}
}

// A restart cannot have disrupted an agent that was not running steadily before
// it, such as one in a crash loop, ready only between crashes. The agent does
// not decide the restart then.
func TestAgentNotRunningSteadilyIsNotChecked(t *testing.T) {
	disrupted := errors.New("the Portainer agent's container was restarted, so the restart disrupted it")
	cases := []struct {
		name    string
		before  *agentState
		checked bool
		logged  bool
	}{
		{"not ready", &agentState{deployment: &appsv1.Deployment{}, pod: "p1", container: "c1"}, false, true},
		{"ready between crashes", &agentState{deployment: &appsv1.Deployment{}, pod: "p1", container: "c1", ready: true, lastExit: time.Now().Add(-10 * time.Second)}, false, true},
		{"no agent", &agentState{}, false, false},
		{"exited long ago", &agentState{deployment: &appsv1.Deployment{}, pod: "p1", container: "c1", ready: true, lastExit: time.Now().Add(-time.Hour)}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agent := &fakeAgent{before: c.before, verdicts: []error{disrupted}}
			last := finished(t, newRestartExecutor(t, nil), host(gates(), agent))
			if checked := agent.verified > 0; checked != c.checked {
				t.Errorf("agent checked: %v, want %v (result %s, %q)", checked, c.checked, last.Result, last.Error)
			}
			if want := map[bool]upgrade.Result{true: upgrade.ResultFailed, false: upgrade.ResultSucceeded}[c.checked]; last.Result != want {
				t.Errorf("result = %s, want %s", last.Result, want)
			}
			if logged := strings.Contains(strings.Join(last.Messages, "\n"), "was not running steadily before the restart"); logged != c.logged {
				t.Errorf("logged that the agent was not running: %v, want %v", logged, c.logged)
			}
		})
	}
}

// HostChanged is saved as KubeSolo is stopped, not before: a run whose executor
// dies earlier, while it reads the agent, did not restart anything.
func TestHostChangedIsSavedAtTheStop(t *testing.T) {
	e := newRestartExecutor(t, nil)
	saved := func() bool {
		st, err := upgrade.LoadState(e.l)
		if err != nil || st.Current == nil {
			t.Fatalf("state: %+v, %v", st, err)
		}
		return st.Current.HostChanged
	}
	var atSnapshot, atStop bool
	h := host(gates(), &fakeAgent{onSnapshot: func() { atSnapshot = saved() }})
	h.stop = func(context.Context) error { atStop = saved(); return nil }

	finished(t, e, h)
	if atSnapshot || !atStop {
		t.Errorf("HostChanged saved: %v while reading the agent, %v at the stop; want false, true", atSnapshot, atStop)
	}
}
