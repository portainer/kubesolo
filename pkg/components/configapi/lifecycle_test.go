package configapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/types"
)

// lifecycleAPI is a running API with the lifecycle endpoints, whose executor
// spawns are recorded instead of run.
type lifecycleAPI struct {
	client *http.Client
	layout upgrade.Layout
	lc     *Lifecycle

	mu     sync.Mutex
	spawns []string
	fail   error
}

func (a *lifecycleAPI) spawned() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.spawns...)
}

func newLifecycleAPI(t *testing.T, running string, seed func(*types.Config), tweak func(*Lifecycle)) *lifecycleAPI {
	t.Helper()
	dir := shortTempDir(t)
	configPath := filepath.Join(dir, "config.yaml")
	cfg := config.Defaults()
	if seed != nil {
		seed(cfg)
	}
	if err := config.Write(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	a := &lifecycleAPI{}
	a.lc = &Lifecycle{
		Version: running,
		Commit:  "abc123",
		DataDir: filepath.Join(dir, "data"),
		Spawn: func(jobFile string) error {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.fail != nil {
				return a.fail
			}
			a.spawns = append(a.spawns, jobFile)
			return nil
		},
		Health: func(context.Context) upgrade.Health {
			return upgrade.Health{Healthy: true, Checks: map[string]string{"apiserver": "ok"}}
		},
		AgentImage: func(context.Context) (string, error) { return "docker.io/portainer/agent:2.30.0", nil },
	}
	if tweak != nil {
		tweak(a.lc)
	}
	a.layout = upgrade.NewLayout(a.lc.DataDir, configPath)

	socket := filepath.Join(dir, "c.sock")
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, cancel, make(chan struct{}), Options{
		SocketPath: socket,
		ConfigPath: configPath,
		Host:       config.Host{NumCPU: 4, GOARCH: "amd64"},
		Lifecycle:  a.lc,
	})
	done := make(chan error, 1)
	go func() { done <- svc.Run() }()
	<-svc.readyCh
	t.Cleanup(func() { cancel(); <-done })

	a.client = unixClient(socket)
	return a
}

func errorOf(t *testing.T, raw []byte) string {
	t.Helper()
	var e ErrorResponse
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("not an error response: %s", raw)
	}
	return e.Error
}

func TestUpgradeIsAcceptedAndHandedToTheExecutor(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.1", nil, nil)

	resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.2", HealthTimeoutSeconds: 120}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	if resp.Header.Get("Location") != "/api/v1/status" {
		t.Errorf("Location = %q", resp.Header.Get("Location"))
	}
	var acc upgrade.Accepted
	if err := json.Unmarshal(raw, &acc); err != nil {
		t.Fatal(err)
	}
	if acc.ID == "" || acc.From != "v1.2.1" || acc.To != "v1.2.2" || acc.Operation != upgrade.OpUpgrade {
		t.Errorf("accepted = %+v", acc)
	}

	spawns := a.spawned()
	if len(spawns) != 1 {
		t.Fatalf("spawned %d executors", len(spawns))
	}
	job, err := upgrade.ReadJob(spawns[0])
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != acc.ID || job.Request.Version != "v1.2.2" || job.Request.HealthTimeoutSeconds != 120 || job.DataDir != a.lc.DataDir || job.From != "v1.2.1" {
		t.Errorf("job = %+v", job)
	}

	state, err := upgrade.LoadState(a.layout)
	if err != nil {
		t.Fatal(err)
	}
	if state.Current == nil || state.Current.ID != acc.ID || state.Current.Phase != upgrade.PhaseQueued {
		t.Errorf("recorded run = %+v", state.Current)
	}
}

// The API is the single writer: a second upgrade while one is in flight, from
// the API or from anything else holding the lock, is refused.
func TestSecondUpgradeIsRefused(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.1", nil, nil)

	resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.2"}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first: %d %s", resp.StatusCode, raw)
	}
	resp, raw = do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.3"}, nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(errorOf(t, raw), "in progress") {
		t.Errorf("second while queued: %d %s", resp.StatusCode, raw)
	}
	resp, raw = do(t, a.client, "POST", "/api/v1/rollback", nil, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("rollback while queued: %d %s", resp.StatusCode, raw)
	}

	// Someone else — kubesoloctl, or the executor itself — holds the lock.
	b := newLifecycleAPI(t, "v1.2.1", nil, nil)
	lock, err := upgrade.TryLock(b.layout)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	resp, raw = do(t, b.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.2"}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("while locked: %d %s", resp.StatusCode, raw)
	}
	if len(b.spawned()) != 0 {
		t.Error("an executor was started while another held the lock")
	}
}

func TestStaleRunDoesNotBlockAnUpgrade(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.1", nil, nil)
	stale := &upgrade.Run{ID: "old", Operation: upgrade.OpUpgrade, Phase: upgrade.PhaseQueued, Started: time.Now().Add(-time.Hour)}
	if err := upgrade.SaveState(a.layout, &upgrade.State{Current: stale}); err != nil {
		t.Fatal(err)
	}
	resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.2"}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d: %s", resp.StatusCode, raw)
	}
}

func TestUpgradeRequestValidation(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.1", nil, nil)
	existing := filepath.Join(shortTempDir(t), "kubesolo.tar.gz")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		body   any
		status int
		want   string
	}{
		{"not JSON", "{", http.StatusBadRequest, "not a valid upgrade request"},
		{"unknown field", `{"version":"v1.2.2","verison":"x"}`, http.StatusBadRequest, "unknown field"},
		{"no version", upgrade.Request{}, http.StatusBadRequest, "version is required"},
		{"bad version", upgrade.Request{Version: "latest"}, http.StatusBadRequest, "not a release version"},
		{"relative source", upgrade.Request{Version: "v1.2.2", Source: "k.tar.gz"}, http.StatusBadRequest, "absolute path"},
		{"missing source", upgrade.Request{Version: "v1.2.2", Source: "/nonexistent/k.tar.gz"}, http.StatusBadRequest, "not a file"},
		{"bad checksum", upgrade.Request{Version: "v1.2.2", SHA256: "xyz"}, http.StatusBadRequest, "not a hex-encoded"},
		{"same version", upgrade.Request{Version: "v1.2.1"}, http.StatusConflict, "already running"},
		{"downgrade", upgrade.Request{Version: "v1.2.0"}, http.StatusConflict, "needs force"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", c.body, nil)
			if resp.StatusCode != c.status || !strings.Contains(errorOf(t, raw), c.want) {
				t.Errorf("status = %d, body %s; want %d containing %q", resp.StatusCode, raw, c.status, c.want)
			}
		})
	}
	if len(a.spawned()) != 0 {
		t.Error("an invalid request started the executor")
	}

	// force allows the downgrade, and a pre-staged source that exists is fine.
	resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.0", Force: true, Source: existing}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("forced downgrade: %d %s", resp.StatusCode, raw)
	}
}

func TestDevBuildNeedsForce(t *testing.T) {
	a := newLifecycleAPI(t, "dev", nil, nil)
	resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.2"}, nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(errorOf(t, raw), "not a release") {
		t.Errorf("status = %d: %s", resp.StatusCode, raw)
	}
}

func TestContainerModeCannotUpgradeItself(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.1", nil, func(lc *Lifecycle) { lc.ContainerMode = true })
	for _, path := range []string{"/api/v1/upgrade", "/api/v1/rollback"} {
		resp, raw := do(t, a.client, "POST", path, upgrade.Request{Version: "v1.2.2"}, nil)
		if resp.StatusCode != http.StatusNotImplemented || !strings.Contains(errorOf(t, raw), "kubesoloctl upgrade") {
			t.Errorf("%s: %d %s", path, resp.StatusCode, raw)
		}
	}
	resp, raw := do(t, a.client, "GET", "/api/v1/status", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status endpoint in container mode: %d %s", resp.StatusCode, raw)
	}
}

func TestSpawnFailureLeavesNothingInFlight(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.1", nil, nil)
	a.fail = errors.New("systemd-run: not found")
	resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.2"}, nil)
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(errorOf(t, raw), "systemd-run") {
		t.Errorf("status = %d: %s", resp.StatusCode, raw)
	}
	state, _ := upgrade.LoadState(a.layout)
	if state.InFlight() {
		t.Error("a run that never started is recorded as in flight")
	}
	jobs, _ := filepath.Glob(filepath.Join(a.layout.Dir(), "job-*.json"))
	if len(jobs) != 0 {
		t.Errorf("job files left behind: %v", jobs)
	}

	a.fail = nil
	if resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.2"}, nil); resp.StatusCode != http.StatusAccepted {
		t.Errorf("retry after spawn failure: %d %s", resp.StatusCode, raw)
	}
}

func writeTestBackup(t *testing.T, l upgrade.Layout, m upgrade.Manifest) {
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

func TestRollback(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, nil)

	resp, raw := do(t, a.client, "POST", "/api/v1/rollback", nil, nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(errorOf(t, raw), "no backup") {
		t.Fatalf("without a backup: %d %s", resp.StatusCode, raw)
	}

	writeTestBackup(t, a.layout, upgrade.Manifest{From: "v1.2.1", To: "v1.2.3"})
	resp, raw = do(t, a.client, "POST", "/api/v1/rollback", nil, nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(errorOf(t, raw), "lose everything") {
		t.Fatalf("backup of another version: %d %s", resp.StatusCode, raw)
	}

	writeTestBackup(t, a.layout, upgrade.Manifest{From: "v1.2.1", To: "v1.2.2"})
	resp, raw = do(t, a.client, "POST", "/api/v1/rollback", map[string]any{"bogus": 1}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: %d %s", resp.StatusCode, raw)
	}

	for _, seconds := range []any{1, -1, uint64(18446744134)} {
		resp, raw = do(t, a.client, "POST", "/api/v1/rollback", map[string]any{"healthTimeoutSeconds": seconds}, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("healthTimeoutSeconds %v: %d %s", seconds, resp.StatusCode, raw)
		}
	}
	if len(a.spawned()) != 0 {
		t.Fatal("a rejected rollback started the executor")
	}

	resp, raw = do(t, a.client, "POST", "/api/v1/rollback", map[string]any{"healthTimeoutSeconds": 90}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("valid rollback: %d %s", resp.StatusCode, raw)
	}
	job, err := upgrade.ReadJob(a.spawned()[0])
	if err != nil {
		t.Fatal(err)
	}
	if job.Operation != upgrade.OpRollback || job.Request.Version != "v1.2.1" || job.Request.HealthTimeoutSeconds != 90 || job.From != "v1.2.2" {
		t.Errorf("job = %+v", job)
	}
	state, _ := upgrade.LoadState(a.layout)
	if msgs := strings.Join(state.Current.Messages, "\n"); !strings.Contains(msgs, "changes made since are lost") {
		t.Errorf("the run does not record that a rollback loses writes:\n%s", msgs)
	}
}

// A restart is accepted, handed to the executor and recorded where
// /api/v1/status reports it, before anything is restarted.
func TestRestartIsAcceptedAndReportedByStatus(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, nil)

	resp, raw := do(t, a.client, "POST", "/api/v1/restart", map[string]any{"healthTimeoutSeconds": 90}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	if resp.Header.Get("Location") != "/api/v1/status" {
		t.Errorf("Location = %q", resp.Header.Get("Location"))
	}
	var acc upgrade.Accepted
	if err := json.Unmarshal(raw, &acc); err != nil {
		t.Fatal(err)
	}
	if acc.ID == "" || acc.Operation != upgrade.OpRestart || acc.From != "v1.2.2" || acc.To != "v1.2.2" || acc.StatusPath != "/api/v1/status" {
		t.Errorf("accepted = %+v", acc)
	}

	spawns := a.spawned()
	if len(spawns) != 1 {
		t.Fatalf("spawned %d executors", len(spawns))
	}
	job, err := upgrade.ReadJob(spawns[0])
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != acc.ID || job.Operation != upgrade.OpRestart || job.Request.HealthTimeoutSeconds != 90 || job.From != "v1.2.2" || job.ConfigFile != a.layout.ConfigFile {
		t.Errorf("job = %+v", job)
	}

	_, raw = do(t, a.client, "GET", "/api/v1/status", nil, nil)
	var st upgrade.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Upgrade.InFlight || st.Upgrade.Current == nil || st.Upgrade.Current.ID != acc.ID || st.Upgrade.Current.Operation != upgrade.OpRestart || st.Upgrade.Current.Phase != upgrade.PhaseQueued {
		t.Errorf("status while queued = %+v", st.Upgrade)
	}

	// The restart holds the same lock as an upgrade: neither can start into
	// the other.
	if resp, raw := do(t, a.client, "POST", "/api/v1/restart", nil, nil); resp.StatusCode != http.StatusConflict || !strings.Contains(errorOf(t, raw), "in progress") {
		t.Errorf("second restart: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := do(t, a.client, "POST", "/api/v1/upgrade", upgrade.Request{Version: "v1.2.3"}, nil); resp.StatusCode != http.StatusConflict {
		t.Errorf("upgrade during a restart: %d %s", resp.StatusCode, raw)
	}
}

// Whatever the executor records at the end of the restart is what status
// reports: a restart that left KubeSolo unhealthy reads as failed.
func TestStatusReportsAFailedRestart(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, nil)
	last := &upgrade.Run{
		ID: "r1", Operation: upgrade.OpRestart, From: "v1.2.2", To: "v1.2.2",
		Phase: upgrade.PhaseDone, Result: upgrade.ResultFailed, HostChanged: true, Finished: time.Now().UTC(),
		Error: "KubeSolo v1.2.2 did not become healthy after the restart: not healthy after 1m0s: node not Ready",
	}
	if err := upgrade.SaveState(a.layout, &upgrade.State{Last: last}); err != nil {
		t.Fatal(err)
	}

	_, raw := do(t, a.client, "GET", "/api/v1/status", nil, nil)
	var st upgrade.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Upgrade.InFlight || st.Upgrade.Last == nil || st.Upgrade.Last.Operation != upgrade.OpRestart || st.Upgrade.Last.Result != upgrade.ResultFailed || st.Upgrade.Last.Error == "" {
		t.Errorf("upgrade = %+v", st.Upgrade)
	}
}

func TestRestartRequestValidation(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, nil)

	cases := []struct {
		name   string
		body   any
		status int
		want   string
	}{
		{"not JSON", "{", http.StatusBadRequest, "not a valid restart request"},
		{"unknown field", map[string]any{"version": "v1.2.3"}, http.StatusBadRequest, "not a valid restart request"},
		{"timeout too short", map[string]any{"healthTimeoutSeconds": 1}, http.StatusBadRequest, "at least"},
		{"negative timeout", map[string]any{"healthTimeoutSeconds": -1}, http.StatusBadRequest, "negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, raw := do(t, a.client, "POST", "/api/v1/restart", c.body, nil)
			if resp.StatusCode != c.status || !strings.Contains(errorOf(t, raw), c.want) {
				t.Errorf("status = %d, body %s; want %d containing %q", resp.StatusCode, raw, c.status, c.want)
			}
		})
	}
	if len(a.spawned()) != 0 {
		t.Error("an invalid request started the executor")
	}
}

// A configuration KubeSolo would refuse at start is refused here, before
// anything is stopped.
func TestRestartRefusesAConfigurationKubeSoloWouldNotStartWith(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, nil)

	// Written behind the API's back, as a hand edit would be: the API itself
	// would have refused it.
	cfg := config.Defaults()
	cfg.D2K.Enabled = true
	cfg.Network.LoadBalancer.Enabled = false
	if err := config.Write(a.layout.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	resp, raw := do(t, a.client, "POST", "/api/v1/restart", nil, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(errorOf(t, raw), "would not start") || !strings.Contains(errorOf(t, raw), "d2k.enabled") {
		t.Errorf("invalid configuration: %d %s", resp.StatusCode, raw)
	}

	if err := os.WriteFile(a.layout.ConfigFile, []byte("network: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, raw = do(t, a.client, "POST", "/api/v1/restart", nil, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(errorOf(t, raw), "would not start") {
		t.Errorf("unreadable configuration: %d %s", resp.StatusCode, raw)
	}

	if len(a.spawned()) != 0 {
		t.Error("a restart into a broken configuration started the executor")
	}
	if state, _ := upgrade.LoadState(a.layout); state.Current != nil {
		t.Errorf("a refused restart was recorded: %+v", state.Current)
	}
}

func TestContainerModeCannotRestartItself(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, func(lc *Lifecycle) { lc.ContainerMode = true })
	resp, raw := do(t, a.client, "POST", "/api/v1/restart", nil, nil)
	if resp.StatusCode != http.StatusNotImplemented || !strings.Contains(errorOf(t, raw), "restart the container from the host") {
		t.Errorf("%d %s", resp.StatusCode, raw)
	}
	if len(a.spawned()) != 0 {
		t.Error("container mode started the executor")
	}
}

func TestStatus(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", func(c *types.Config) {
		c.Portainer.EdgeID, c.Portainer.EdgeKey = "id", "key"
		c.Portainer.Image = "docker.io/portainer/agent:2.31.0"
	}, func(lc *Lifecycle) {
		lc.Health = func(context.Context) upgrade.Health {
			return upgrade.Health{Healthy: false, Checks: map[string]string{"apiserver": "ok", "node": "not Ready"}}
		}
	})
	writeTestBackup(t, a.layout, upgrade.Manifest{From: "v1.2.1", To: "v1.2.2"})
	last := &upgrade.Run{ID: "r1", Operation: upgrade.OpUpgrade, From: "v1.2.1", To: "v1.2.2", Result: upgrade.ResultSucceeded}
	if err := upgrade.SaveState(a.layout, &upgrade.State{Last: last}); err != nil {
		t.Fatal(err)
	}

	resp, raw := do(t, a.client, "GET", "/api/v1/status", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var st upgrade.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Version != "v1.2.2" || st.Commit != "abc123" {
		t.Errorf("version = %q commit = %q", st.Version, st.Commit)
	}
	if st.Health.Healthy || st.Health.Checks["node"] != "not Ready" {
		t.Errorf("health = %+v", st.Health)
	}
	if st.Upgrade.Last == nil || st.Upgrade.Last.ID != "r1" || st.Upgrade.RollbackTarget == nil || st.Upgrade.RollbackTarget.From != "v1.2.1" {
		t.Errorf("upgrade = %+v", st.Upgrade)
	}
	// Configured one agent version, cluster runs another: reported, not hidden.
	if st.Agent == nil || st.Agent.InSync || st.Agent.ConfiguredImage != "docker.io/portainer/agent:2.31.0" || st.Agent.RunningImage != "docker.io/portainer/agent:2.30.0" || st.Agent.Detail == "" {
		t.Errorf("agent = %+v", st.Agent)
	}
}

func TestStatusWithoutAgentOmitsIt(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, nil)
	_, raw := do(t, a.client, "GET", "/api/v1/status", nil, nil)
	var st upgrade.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Agent != nil {
		t.Errorf("agent reported with none configured: %+v", st.Agent)
	}
	if st.Upgrade.RollbackUnavailable == "" {
		t.Error("no reason given for the missing rollback target")
	}
}

func TestLifecycleEndpointsAbsentWithoutLifecycle(t *testing.T) {
	client, _ := api(t, nil)
	for _, c := range []struct{ method, path string }{{"POST", "/api/v1/upgrade"}, {"POST", "/api/v1/rollback"}, {"POST", "/api/v1/restart"}, {"GET", "/api/v1/status"}} {
		if resp, _ := do(t, client, c.method, c.path, upgrade.Request{Version: "v1.2.2"}, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestClientLifecycleMethods(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.1", nil, nil)
	c := NewClient(filepath.Join(filepath.Dir(a.layout.ConfigFile), "c.sock"))

	st, err := c.Status()
	if err != nil || st.Version != "v1.2.1" {
		t.Fatalf("Status: %+v, %v", st, err)
	}
	acc, err := c.Upgrade(upgrade.Request{Version: "v1.2.2"})
	if err != nil || acc.To != "v1.2.2" {
		t.Fatalf("Upgrade: %+v, %v", acc, err)
	}
	_, err = c.Upgrade(upgrade.Request{Version: "v1.2.3"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Errorf("second Upgrade: %v", err)
	}
	if _, err := c.Rollback(0); err == nil {
		t.Error("Rollback with no backup succeeded")
	}
}

// The agent's socket serves the same API from a directory of its own, and both
// sockets are gone once the API stops.
func TestAgentSocket(t *testing.T) {
	dir := shortTempDir(t)
	configPath := filepath.Join(dir, "config.yaml")
	if err := config.Write(configPath, config.Defaults()); err != nil {
		t.Fatal(err)
	}
	main, agent := filepath.Join(dir, "c.sock"), filepath.Join(dir, "agent", "k.sock")

	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, cancel, make(chan struct{}), Options{
		SocketPath: main, AgentSocketPath: agent, ConfigPath: configPath,
		Host: config.Host{NumCPU: 4, GOARCH: "amd64"},
	})
	done := make(chan error, 1)
	go func() { done <- svc.Run() }()
	<-svc.readyCh

	for _, socket := range []string{main, agent} {
		if resp, raw := do(t, unixClient(socket), "GET", "/api/v1/config", nil, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", socket, resp.StatusCode, raw)
		}
		if fi, err := os.Stat(socket); err != nil || fi.Mode().Perm() != socketMode {
			t.Errorf("%s mode: %v %v", socket, fi.Mode(), err)
		}
	}

	cancel()
	<-done
	for _, socket := range []string{main, agent} {
		if _, err := os.Stat(socket); !os.IsNotExist(err) {
			t.Errorf("%s left behind: %v", socket, err)
		}
	}
}

// The Portainer agent restarts KubeSolo through its own socket, so the restart
// endpoint is served there exactly as on the main one.
func TestRestartThroughTheAgentSocket(t *testing.T) {
	dir := shortTempDir(t)
	configPath := filepath.Join(dir, "config.yaml")
	if err := config.Write(configPath, config.Defaults()); err != nil {
		t.Fatal(err)
	}
	spawned := make(chan string, 2)
	lc := &Lifecycle{
		Version: "v1.2.2",
		DataDir: filepath.Join(dir, "data"),
		Spawn:   func(jobFile string) error { spawned <- jobFile; return nil },
		Health: func(context.Context) upgrade.Health {
			return upgrade.Health{Healthy: true, Checks: map[string]string{"apiserver": "ok"}}
		},
		AgentImage: func(context.Context) (string, error) { return "", nil },
	}
	agentSocket := filepath.Join(dir, "agent", "k.sock")
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, cancel, make(chan struct{}), Options{
		SocketPath: filepath.Join(dir, "c.sock"), AgentSocketPath: agentSocket, ConfigPath: configPath,
		Host: config.Host{NumCPU: 4, GOARCH: "amd64"}, Lifecycle: lc,
	})
	done := make(chan error, 1)
	go func() { done <- svc.Run() }()
	<-svc.readyCh
	t.Cleanup(func() { cancel(); <-done })

	agent := unixClient(agentSocket)
	resp, raw := do(t, agent, "POST", "/api/v1/restart", nil, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart through the agent socket: %d %s", resp.StatusCode, raw)
	}
	var acc upgrade.Accepted
	if err := json.Unmarshal(raw, &acc); err != nil || acc.Operation != upgrade.OpRestart {
		t.Fatalf("accepted = %+v, %v", acc, err)
	}
	if job, err := upgrade.ReadJob(<-spawned); err != nil || job.ID != acc.ID || job.Operation != upgrade.OpRestart {
		t.Fatalf("job = %+v, %v", job, err)
	}

	// The agent follows it on the same socket.
	_, raw = do(t, agent, "GET", "/api/v1/status", nil, nil)
	var st upgrade.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Upgrade.Current == nil || st.Upgrade.Current.ID != acc.ID {
		t.Errorf("status on the agent socket = %+v", st.Upgrade)
	}
}

// The API refuses a change to the agent's set-once settings, but the file can
// be edited by hand. A restart would ignore the change and report success, so
// it is refused.
func TestRestartRefusesAHandEditedAgentSetting(t *testing.T) {
	running := config.Defaults()
	running.Portainer.EdgeID, running.Portainer.EdgeKey = "edge-id", "edge-key"
	a := newLifecycleAPI(t, "v1.2.2", func(c *types.Config) {
		c.Portainer.EdgeID, c.Portainer.EdgeKey = "edge-id", "edge-key"
	}, func(lc *Lifecycle) { lc.StartedWith = running })

	if resp, raw := do(t, a.client, "POST", "/api/v1/restart", nil, nil); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unchanged agent settings: %d %s", resp.StatusCode, raw)
	}
	state, _ := upgrade.LoadState(a.layout)
	state.Current = nil
	if err := upgrade.SaveState(a.layout, state); err != nil {
		t.Fatal(err)
	}

	edited := config.Defaults()
	edited.Portainer.EdgeID, edited.Portainer.EdgeKey = "edge-id", "another-key"
	if err := config.Write(a.layout.ConfigFile, edited); err != nil {
		t.Fatal(err)
	}
	resp, raw := do(t, a.client, "POST", "/api/v1/restart", nil, nil)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(errorOf(t, raw), "portainer.edgeKey") || !strings.Contains(errorOf(t, raw), "Put back the value it started with") {
		t.Errorf("hand-edited edge key: %d %s", resp.StatusCode, raw)
	}
	if len(a.spawned()) != 1 {
		t.Errorf("spawned %d executors, want only the first restart's", len(a.spawned()))
	}

	// Configuring an agent for the first time is applied by the restart.
	b := newLifecycleAPI(t, "v1.2.2", func(c *types.Config) {
		c.Portainer.EdgeID, c.Portainer.EdgeKey = "edge-id", "edge-key"
	}, func(lc *Lifecycle) { lc.StartedWith = config.Defaults() })
	if resp, raw := do(t, b.client, "POST", "/api/v1/restart", nil, nil); resp.StatusCode != http.StatusAccepted {
		t.Errorf("first agent configuration: %d %s", resp.StatusCode, raw)
	}
}

// A restart may revert the configuration file, so a change made while one runs
// would be lost. It is refused until the run ends.
func TestConfigChangesWaitForARunToEnd(t *testing.T) {
	a := newLifecycleAPI(t, "v1.2.2", nil, nil)
	if resp, raw := do(t, a.client, "POST", "/api/v1/restart", nil, nil); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart: %d %s", resp.StatusCode, raw)
	}
	patch := map[string]string{"Content-Type": "application/merge-patch+json"}
	resp, raw := do(t, a.client, "PATCH", "/api/v1/config", `{"network":{"mtu":1400}}`, patch)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(errorOf(t, raw), "restart is in progress") {
		t.Errorf("change during a restart: %d %s", resp.StatusCode, raw)
	}

	state, _ := upgrade.LoadState(a.layout)
	state.Current = nil
	if err := upgrade.SaveState(a.layout, state); err != nil {
		t.Fatal(err)
	}
	if resp, raw := do(t, a.client, "PATCH", "/api/v1/config", `{"network":{"mtu":1400}}`, patch); resp.StatusCode != http.StatusOK {
		t.Errorf("change once the restart has ended: %d %s", resp.StatusCode, raw)
	}
}

// The agent is deployed from the configuration KubeSolo starts with. Until it
// has started with one, a mistyped edge ID or key can still be corrected.
func TestAgentSettingsCanBeCorrectedUntilKubeSoloStartsWithThem(t *testing.T) {
	agent := func(c *types.Config) { c.Portainer.EdgeID, c.Portainer.EdgeKey = "edge-id-typo", "edge-key" }
	patch := map[string]string{"Content-Type": "application/merge-patch+json"}
	fix := `{"portainer":{"edgeID":"edge-id"}}`

	notStarted := newLifecycleAPI(t, "v1.2.2", agent, func(lc *Lifecycle) { lc.StartedWith = config.Defaults() })
	if resp, raw := do(t, notStarted.client, "PATCH", "/api/v1/config", fix, patch); resp.StatusCode != http.StatusOK {
		t.Errorf("before KubeSolo started with an agent: %d %s", resp.StatusCode, raw)
	}

	started := config.Defaults()
	agent(started)
	deployed := newLifecycleAPI(t, "v1.2.2", agent, func(lc *Lifecycle) { lc.StartedWith = started })
	if resp, raw := do(t, deployed.client, "PATCH", "/api/v1/config", fix, patch); resp.StatusCode != http.StatusConflict {
		t.Errorf("after KubeSolo started with an agent: %d %s", resp.StatusCode, raw)
	}
}
