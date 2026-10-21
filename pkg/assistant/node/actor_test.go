package node

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/pkg/assistant"

	_ "github.com/mattn/go-sqlite3"
)

func testActor(t *testing.T, failing map[string]error) (*Actor, *[]string) {
	t.Helper()
	dir := t.TempDir()
	l := upgrade.Layout{DataDir: filepath.Join(dir, "data"), Binary: filepath.Join(dir, "kubesolo"), ConfigFile: filepath.Join(dir, "config.yaml")}
	if err := os.MkdirAll(l.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func(context.Context) error{}
	for _, name := range []string{"kine", "kubelet", assistant.CheckDisk} {
		err := failing[name]
		checks[name] = func(context.Context) error { return err }
	}
	var spawned []string
	return &Actor{
		Checks:  checks,
		Layout:  l,
		Version: "v1.2.2",
		Spawn:   func(jobFile string) error { spawned = append(spawned, jobFile); return nil },
	}, &spawned
}

var incident = assistant.Incident{ID: "20261021T030000.000000000Z", Trigger: "unhealthy for 5m0s: kubelet"}

func TestARestartNeedsAComponentStillFailing(t *testing.T) {
	healthy, _ := testActor(t, nil)
	if err := healthy.Check(context.Background(), "restart_component", incident); err == nil || !strings.Contains(err.Error(), "healthy") {
		t.Errorf("all healthy: got %v, want a refusal", err)
	}
	// A full disk is no reason to restart.
	full, _ := testActor(t, map[string]error{assistant.CheckDisk: errors.New("99% used")})
	if err := full.Check(context.Background(), "restart_component", incident); err == nil {
		t.Error("a full disk alone should not allow a restart")
	}
	down, _ := testActor(t, map[string]error{"kubelet": errors.New("connection refused")})
	if err := down.Check(context.Background(), "restart_component", incident); err != nil {
		t.Errorf("kubelet failing: got %v, want nil", err)
	}
}

func TestARestartIsLeftToTheExecutor(t *testing.T) {
	a, spawned := testActor(t, map[string]error{"kubelet": errors.New("down")})

	job, err := a.Run(context.Background(), "restart_component", incident)
	if err != nil {
		t.Fatal(err)
	}
	if len(*spawned) != 1 || job == "" {
		t.Fatalf("job %q, spawned %v", job, *spawned)
	}
	j, err := upgrade.ReadJob((*spawned)[0])
	if err != nil || j.Operation != upgrade.OpRestart || j.ID != job {
		t.Errorf("job = %+v, %v", j, err)
	}
	if done, _ := a.Settle(context.Background(), job); done {
		t.Error("settled while the restart is queued")
	}

	// The executor records the outcome as the last run.
	state, _ := upgrade.LoadState(a.Layout)
	state.Last, state.Current = state.Current, nil
	state.Last.Result = upgrade.ResultSucceeded
	if err := upgrade.SaveState(a.Layout, state); err != nil {
		t.Fatal(err)
	}
	a.Checks["kubelet"] = func(context.Context) error { return nil }
	if done, err := a.Settle(context.Background(), job); !done || err != nil {
		t.Errorf("after success: done %v, err %v", done, err)
	}

	state.Last.Result, state.Last.Error = upgrade.ResultFailed, "KubeSolo did not become healthy after the restart"
	_ = upgrade.SaveState(a.Layout, state)
	if done, err := a.Settle(context.Background(), job); !done || err == nil || !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("after failure: done %v, err %v", done, err)
	}
}

// A restart succeeds only if it fixed the fault: KubeSolo coming back healthy
// is not enough while the component it was for still fails. Components get a
// while to come up first.
func TestARestartThatLeavesTheComponentFailingFailed(t *testing.T) {
	a, _ := testActor(t, map[string]error{"kubelet": errors.New("connection refused")})
	job, err := a.Run(context.Background(), "restart_component", incident)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := upgrade.LoadState(a.Layout)
	state.Last, state.Current = state.Current, nil
	state.Last.Result, state.Last.Finished = upgrade.ResultSucceeded, time.Now()
	_ = upgrade.SaveState(a.Layout, state)
	if done, _ := a.Settle(context.Background(), job); done {
		t.Error("settled before the components had a while to come up")
	}

	state.Last.Finished = time.Now().Add(-restartSettleTime - time.Second)
	_ = upgrade.SaveState(a.Layout, state)
	if done, err := a.Settle(context.Background(), job); !done || err == nil || !strings.Contains(err.Error(), "kubelet: connection refused") {
		t.Errorf("done %v, err %v; want a failure naming the kubelet", done, err)
	}
}

func TestActionsNotCarriedOutYetAreRefused(t *testing.T) {
	a, spawned := testActor(t, map[string]error{"kine": errors.New("down")})
	for _, action := range []string{"free_disk_space", "report_only"} {
		if err := a.Check(context.Background(), action, incident); err == nil {
			t.Errorf("%s: want a refusal", action)
		}
	}
	if len(*spawned) != 0 {
		t.Error("something was started")
	}
}

// writeDatastore puts an SQLite database where kine keeps it, intact or with
// its pages overwritten.
func writeDatastore(t *testing.T, a *Actor, damaged bool) {
	t.Helper()
	path := a.Layout.Datastore()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE kine (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO kine (name) VALUES ('/registry/pods/x')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if damaged {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt(bytes.Repeat([]byte{0xff}, 4096), 4096); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
}

// A sound datastore is never repaired: repairing drops its latest writes.
func TestARepairNeedsADamagedDatastore(t *testing.T) {
	sound, _ := testActor(t, nil)
	writeDatastore(t, sound, false)
	if err := sound.Check(context.Background(), "repair_database", incident); err == nil || !strings.Contains(err.Error(), "integrity check") {
		t.Errorf("sound: got %v, want a refusal", err)
	}

	damaged, spawned := testActor(t, nil)
	writeDatastore(t, damaged, true)
	if err := damaged.Check(context.Background(), "repair_database", incident); err != nil {
		t.Fatalf("damaged: got %v, want nil", err)
	}
	if _, err := damaged.Run(context.Background(), "repair_database", incident); err != nil {
		t.Fatal(err)
	}
	j, err := upgrade.ReadJob((*spawned)[0])
	if err != nil || j.Operation != upgrade.OpRestart || !j.Request.RepairDatastore {
		t.Errorf("job = %+v, %v; want a restart that repairs the datastore", j, err)
	}
}
