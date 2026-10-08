package upgrade

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLayout(t *testing.T) Layout {
	t.Helper()
	dir := t.TempDir()
	l := Layout{
		DataDir:    filepath.Join(dir, "data"),
		Binary:     filepath.Join(dir, "bin", "kubesolo"),
		ConfigFile: filepath.Join(dir, "etc", "config.yaml"),
	}
	for _, d := range []string{l.DataDir, filepath.Dir(l.Binary), filepath.Dir(l.ConfigFile), l.DatastoreDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestParseVersion(t *testing.T) {
	for _, ok := range []string{"v1.2.1", "v0.0.1", "v1.3.0-rc.1", "v1.3.0-test.2", "v10.20.30"} {
		if _, err := ParseVersion(ok); err != nil {
			t.Errorf("ParseVersion(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1.2.1", "v1.2", "v1.2.1.4", "dev", "v01.2.3", "v1.2.1 ", "latest"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) accepted", bad)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	ordered := []string{"v1.1.9", "v1.2.0", "v1.2.1-rc.1", "v1.2.1-rc.2", "v1.2.1-rc.10", "v1.2.1", "v1.2.2-test.1", "v1.2.2-test.2", "v1.2.2", "v1.10.0", "v2.0.0"}
	for i := range ordered {
		for j := range ordered {
			a, _ := ParseVersion(ordered[i])
			b, _ := ParseVersion(ordered[j])
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("%s vs %s = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func TestReportedVersion(t *testing.T) {
	out := `{"level":"info","version":"v1.2.1","time":"2026-10-07T21:43:44Z","message":"kubesolo version"}`
	if v, ok := ReportedVersion(out); !ok || v != "v1.2.1" {
		t.Errorf("ReportedVersion = %q, %v", v, ok)
	}
	if _, ok := ReportedVersion("kubesolo: error: unknown long flag '--version'"); ok {
		t.Error("found a version in an error")
	}
}

func TestStateRoundTripAndInFlight(t *testing.T) {
	l := testLayout(t)

	s, err := LoadState(l)
	if err != nil || s.Current != nil || s.Last != nil {
		t.Fatalf("missing state file: %+v, %v", s, err)
	}
	if s.InFlight() {
		t.Error("empty state is in flight")
	}

	run := &Run{ID: "abc", Operation: OpUpgrade, From: "v1.2.1", To: "v1.2.2", Phase: PhaseQueued, Started: time.Now()}
	run.Log("accepted %s", "here")
	s.Current = run
	if err := SaveState(l, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(l)
	if err != nil {
		t.Fatal(err)
	}
	if got.Current.ID != "abc" || len(got.Current.Messages) != 1 || !strings.HasSuffix(got.Current.Messages[0], "accepted here") {
		t.Errorf("round trip lost the run: %+v", got.Current)
	}
	if fi, err := os.Stat(l.StateFile()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode: %v, %v", fi.Mode(), err)
	}

	// Queued moments ago, executor not started yet: in flight.
	if !got.InFlight() {
		t.Error("freshly queued run is not in flight")
	}
	// Queued long ago and never claimed: stale.
	got.Current.Started = time.Now().Add(-time.Hour)
	if got.InFlight() {
		t.Error("abandoned queued run is in flight")
	}
	// Claimed by a live executor.
	got.Current.PID = os.Getpid()
	if !got.InFlight() {
		t.Error("run with a live executor is not in flight")
	}
	// Claimed by an executor that has died.
	got.Current.PID = deadPID(t)
	if got.InFlight() {
		t.Error("run with a dead executor is in flight")
	}
}

// deadPID returns a PID that is not running.
func deadPID(t *testing.T) int {
	t.Helper()
	for pid := 4_000_000; pid > 3_000_000; pid -= 7919 {
		if !processAlive(pid) {
			return pid
		}
	}
	t.Skip("no free pid found")
	return 0
}

func TestStateUnreadable(t *testing.T) {
	l := testLayout(t)
	if err := os.MkdirAll(l.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.StateFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(l); err == nil {
		t.Error("corrupt state file loaded")
	}
}

func TestRunLogIsBounded(t *testing.T) {
	var r Run
	for i := 0; i < maxMessages+20; i++ {
		r.Log("message %d", i)
	}
	if len(r.Messages) != maxMessages || !strings.HasSuffix(r.Messages[len(r.Messages)-1], "message 69") {
		t.Errorf("messages = %d, last %q", len(r.Messages), r.Messages[len(r.Messages)-1])
	}
}

func TestLockIsExclusive(t *testing.T) {
	l := testLayout(t)
	first, err := TryLock(l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(l); !errors.Is(err, ErrBusy) {
		t.Fatalf("second TryLock = %v, want ErrBusy", err)
	}
	start := time.Now()
	if _, err := WaitLock(l, 300*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("WaitLock while held = %v, want ErrBusy", err)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Error("WaitLock gave up early")
	}
	first.Unlock()
	second, err := WaitLock(l, time.Second)
	if err != nil {
		t.Fatalf("WaitLock after release: %v", err)
	}
	second.Unlock()
	second.Unlock() // idempotent
}

func TestRequestValidate(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	cases := []struct {
		req Request
		ok  bool
	}{
		{Request{Version: "v1.2.2"}, true},
		{Request{Version: "v1.2.2", Source: "/tmp/k.tar.gz", SHA256: sum}, true},
		{Request{}, false},
		{Request{Version: "1.2.2"}, false},
		{Request{Version: "v1.2.2", Source: "relative/k.tar.gz"}, false},
		{Request{Version: "v1.2.2", SHA256: "abc"}, false},
		{Request{Version: "v1.2.2", SHA256: strings.Repeat("zz", 32)}, false},
		{Request{Version: "v1.2.2", HealthTimeoutSeconds: -1}, false},
	}
	for _, c := range cases {
		if err := c.req.Validate(); (err == nil) != c.ok {
			t.Errorf("Validate(%+v) = %v, want ok=%v", c.req, err, c.ok)
		}
	}
	if (Request{}).HealthTimeout() != DefaultHealthTimeout || (Request{HealthTimeoutSeconds: 30}).HealthTimeout() != 30*time.Second {
		t.Error("HealthTimeout")
	}
}

func writeBackup(t *testing.T, l Layout, m Manifest) {
	t.Helper()
	if err := os.MkdirAll(l.BackupDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{l.BackupBinary(), l.BackupDatastore()} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(l.BackupManifest(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A rollback restores the datastore as it was when the running version was
// installed. Any later upgrade makes that snapshot the wrong one.
func TestRollbackTarget(t *testing.T) {
	l := testLayout(t)
	if _, err := RollbackTarget(l, "v1.2.2"); err == nil || !strings.Contains(err.Error(), "no backup") {
		t.Errorf("no backup: %v", err)
	}

	writeBackup(t, l, Manifest{From: "v1.2.1", To: "v1.2.2", Created: time.Now()})
	m, err := RollbackTarget(l, "v1.2.2")
	if err != nil || m.From != "v1.2.1" {
		t.Fatalf("valid backup: %+v, %v", m, err)
	}
	if _, err := RollbackTarget(l, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "lose everything") {
		t.Errorf("backup for another version: %v", err)
	}

	_ = os.Remove(l.BackupDatastore())
	if _, err := RollbackTarget(l, "v1.2.2"); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("incomplete backup: %v", err)
	}
}

func TestReadUpgradeStatus(t *testing.T) {
	l := testLayout(t)
	writeBackup(t, l, Manifest{From: "v1.2.1", To: "v1.2.2"})
	if err := os.WriteFile(l.PendingFile(), []byte("v1.2.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.AttemptsFile(), []byte("2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.GuardLog(), []byte("t1 one\n\nt2 two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	last := &Run{ID: "x", Result: ResultSucceeded}
	if err := SaveState(l, &State{Last: last, Current: &Run{ID: "stale", PID: deadPID(t)}}); err != nil {
		t.Fatal(err)
	}

	st := ReadUpgradeStatus(l, "v1.2.2")
	if st.InFlight || st.Current != nil {
		t.Errorf("stale run reported in flight: %+v", st.Current)
	}
	if st.Last == nil || st.Last.ID != "x" {
		t.Errorf("last run: %+v", st.Last)
	}
	if st.PendingVerify != "v1.2.2" || st.BootAttempts != 2 {
		t.Errorf("pending %q attempts %d", st.PendingVerify, st.BootAttempts)
	}
	if st.RollbackTarget == nil || st.RollbackTarget.From != "v1.2.1" {
		t.Errorf("rollback target: %+v (%s)", st.RollbackTarget, st.RollbackUnavailable)
	}
	if len(st.GuardEvents) != 2 {
		t.Errorf("guard events: %q", st.GuardEvents)
	}

	if st := ReadUpgradeStatus(l, "v9.9.9"); st.RollbackTarget != nil || st.RollbackUnavailable == "" {
		t.Error("rollback offered for a backup of another version")
	}
}

func TestJobRoundTrip(t *testing.T) {
	l := testLayout(t)
	j := Job{ID: "id1", Operation: OpUpgrade, Request: Request{Version: "v1.2.2"}, DataDir: l.DataDir, ConfigFile: l.ConfigFile, From: "v1.2.1"}
	path, err := WriteJob(l, j)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadJob(path)
	if err != nil || got.ID != "id1" || got.Request.Version != "v1.2.2" || got.Layout().DataDir != l.DataDir {
		t.Errorf("job round trip: %+v, %v", got, err)
	}
}

func TestProxyEnvAndHelpers(t *testing.T) {
	env := proxyEnv([]string{"PATH=/bin", "HTTP_PROXY=http://p:3128", "https_proxy=http://q", "NO_PROXY=localhost", "SECRET=x"})
	if len(env) != 3 {
		t.Errorf("proxyEnv = %q", env)
	}
	if !isUpgradeHelper([]string{"/usr/local/bin/kubesolo", "--upgrade-executor=/x"}) ||
		!isUpgradeHelper([]string{"kubesolo", "--upgrade-check-datastore=/y"}) ||
		isUpgradeHelper([]string{"kubesolo", "--config=/etc/kubesolo/config.yaml"}) {
		t.Error("isUpgradeHelper")
	}
}

// A run whose executor died is closed by the KubeSolo that comes up afterwards,
// according to which version that is.
func TestCloseInterrupted(t *testing.T) {
	started := time.Now().Add(-5 * time.Minute).UTC()
	interrupted := func(op Operation, hostChanged bool) *State {
		return &State{Current: &Run{ID: "r", Operation: op, From: "v1.2.1", To: "v1.2.2", Started: started, PID: deadPID(t), HostChanged: hostChanged}}
	}
	guard := started.Add(time.Minute).Format(time.RFC3339) + " v1.2.2 failed 3 starts; restored v1.2.1 and its datastore"
	oldGuard := started.Add(-time.Hour).Format(time.RFC3339) + " v1.2.0 failed 3 starts; restored v1.1.9"

	cases := []struct {
		name    string
		state   *State
		running string
		guard   string
		want    Result
	}{
		{"new version came up", interrupted(OpUpgrade, true), "v1.2.2", "", ResultSucceeded},
		{"guard restored the old one", interrupted(OpUpgrade, true), "v1.2.1", guard, ResultRolledBack},
		{"an old guard event is not this run's", interrupted(OpUpgrade, true), "v1.2.1", oldGuard, ResultFailed},
		{"died before changing anything", interrupted(OpUpgrade, false), "v1.2.1", "", ResultAborted},
		{"rollback came up", &State{Current: &Run{ID: "r", Operation: OpRollback, From: "v1.2.2", To: "v1.2.1", Started: started, PID: deadPID(t), HostChanged: true}}, "v1.2.1", "", ResultSucceeded},
		{"something else is running", interrupted(OpUpgrade, true), "v1.1.9", "", ResultFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.state.CloseInterrupted(c.running, c.guard) {
				t.Fatal("not closed")
			}
			if c.state.Current != nil || c.state.Last == nil || c.state.Last.Result != c.want || c.state.Last.Finished.IsZero() {
				t.Errorf("last = %+v, want result %s", c.state.Last, c.want)
			}
		})
	}

	live := &State{Current: &Run{ID: "r", PID: os.Getpid(), Started: started}}
	if live.CloseInterrupted("v1.2.2", "") {
		t.Error("closed a run whose executor is alive")
	}
	if (&State{}).CloseInterrupted("v1.2.2", "") {
		t.Error("closed a run that does not exist")
	}

	l := testLayout(t)
	if err := SaveState(l, interrupted(OpUpgrade, true)); err != nil {
		t.Fatal(err)
	}
	if st := ReadUpgradeStatus(l, "v1.2.1"); st.Interrupted == nil || st.Current != nil || st.InFlight {
		t.Errorf("interrupted run not reported: %+v", st)
	}
}

// A failed upgrade must not cost the rollback target that existed before it.
func TestBackupsSurviveAFailedUpgrade(t *testing.T) {
	l := testLayout(t)
	stage := func(from, to string) string {
		scratch := testLayout(t)
		writeBackup(t, scratch, Manifest{From: from, To: to})
		dir := l.BackupDir() + ".new"
		if err := os.MkdirAll(l.Dir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(scratch.BackupDir(), dir); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	target := func(running string) string {
		m, err := RollbackTarget(l, running)
		if err != nil {
			return "none"
		}
		return m.From
	}

	// v1 → v2 succeeds: rollback goes to v1.
	if err := CommitBackup(l, stage("v1", "v2")); err != nil {
		t.Fatal(err)
	}
	SettleBackups(l, OpUpgrade, ResultSucceeded)
	if got := target("v2"); got != "v1" {
		t.Fatalf("after v1→v2: target %s", got)
	}

	// v2 → v3 is rolled back: v2 runs again, and its rollback target is still v1.
	if err := CommitBackup(l, stage("v2", "v3")); err != nil {
		t.Fatal(err)
	}
	SettleBackups(l, OpUpgrade, ResultRolledBack)
	if got := target("v2"); got != "v1" {
		t.Errorf("after a rolled-back v2→v3: target %s, want v1", got)
	}
	if _, err := os.Stat(l.PreviousBackupDir()); !os.IsNotExist(err) {
		t.Error("previous backup left behind")
	}

	// A failed upgrade keeps both backups for whoever investigates.
	if err := CommitBackup(l, stage("v2", "v4")); err != nil {
		t.Fatal(err)
	}
	SettleBackups(l, OpUpgrade, ResultFailed)
	for _, d := range []string{l.BackupDir(), l.PreviousBackupDir()} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s gone after a failed upgrade", d)
		}
	}
}

// The very first upgrade, rolled back, leaves no target rather than one that
// restores the version already running.
func TestFirstUpgradeRolledBackLeavesNoTarget(t *testing.T) {
	l := testLayout(t)
	writeBackup(t, l, Manifest{From: "v1", To: "v2"})
	if err := os.Rename(l.BackupDir(), l.BackupDir()+".new"); err != nil {
		t.Fatal(err)
	}
	if err := CommitBackup(l, l.BackupDir()+".new"); err != nil {
		t.Fatal(err)
	}
	SettleBackups(l, OpUpgrade, ResultRolledBack)
	if _, err := os.Stat(l.BackupDir()); !os.IsNotExist(err) {
		t.Error("kept a backup of the version that is running again")
	}
}

// A commit that fails leaves the previous backup where it was.
func TestCommitBackupFailureKeepsThePreviousBackup(t *testing.T) {
	l := testLayout(t)
	writeBackup(t, l, Manifest{From: "v1", To: "v2"})
	if err := CommitBackup(l, filepath.Join(l.Dir(), "does-not-exist")); err == nil {
		t.Fatal("commit of a missing backup succeeded")
	}
	if m, err := RollbackTarget(l, "v2"); err != nil || m.From != "v1" {
		t.Errorf("rollback target after a failed commit: %+v, %v", m, err)
	}
	if _, err := os.Stat(l.PreviousBackupDir()); !os.IsNotExist(err) {
		t.Error("previous backup left aside")
	}
}

func TestVersionFromArchiveName(t *testing.T) {
	for name, want := range map[string]string{
		"/tmp/kubesolo-v1.2.2-linux-amd64.tar.gz":              "v1.2.2",
		"kubesolo-v1.2.2-linux-arm64-musl.tar.gz":              "v1.2.2",
		"kubesolo-v1.2.2-linux-amd64-offline.tar.gz":           "v1.2.2",
		"kubesolo-v1.2.2-linux-arm-musl-offline.tar.gz":        "v1.2.2",
		"kubesolo-v1.3.0-rc.1-linux-riscv64-offline.tar.gz":    "v1.3.0-rc.1",
		"kubesolo-v1.3.0-test.9crash-linux-amd64.tgz":          "v1.3.0-test.9crash",
		"kubesolo-v1.3.0-rc.1-linux-amd64-musl-offline.tar.gz": "v1.3.0-rc.1",
	} {
		if got, ok := VersionFromArchiveName(name); !ok || got != want {
			t.Errorf("%s: %q, %v; want %s", name, got, ok, want)
		}
	}
	for _, name := range []string{"kubesolo", "kubesolo-latest-linux-amd64.tar.gz", "other-v1.2.2-linux-amd64.tar.gz"} {
		if got, ok := VersionFromArchiveName(name); ok {
			t.Errorf("%s: matched %q", name, got)
		}
	}
}
