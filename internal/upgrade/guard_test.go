package upgrade

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// guardFixture is a layout with an installed binary, datastore and
// configuration, a backup of different ones, and the guard rendered against
// fake cgroup and proc trees.
type guardFixture struct {
	l     Layout
	guard string
}

func newGuardFixture(t *testing.T) guardFixture {
	t.Helper()
	l := testLayout(t)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(l.Binary, "new binary")
	write(l.Datastore(), "new db")
	write(l.Datastore()+"-wal", "new wal")
	write(l.Datastore()+"-shm", "new shm")
	write(l.ConfigFile, "new config")
	write(l.BackupBinary(), "old binary")
	write(l.BackupDatastore(), "old db")
	write(l.BackupConfigFile(), "old config")
	write(l.BackupManifest(), `{"from": "v1.2.1", "to": "v1.2.2"}`)

	fake := t.TempDir()
	script, err := renderGuard(l, filepath.Join(fake, "cgroup"), filepath.Join(fake, "proc"))
	if err != nil {
		t.Fatal(err)
	}
	guard := filepath.Join(fake, "guard.sh")
	write(guard, string(script))
	return guardFixture{l: l, guard: guard}
}

func (f guardFixture) run(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("/bin/sh", f.guard).CombinedOutput(); err != nil {
		t.Fatalf("guard failed: %v\n%s", err, out)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return strings.TrimSpace(string(raw))
}

func TestGuardDoesNothingWithoutPendingUpgrade(t *testing.T) {
	f := newGuardFixture(t)
	f.run(t)
	if got := readString(t, f.l.Binary); got != "new binary" {
		t.Errorf("binary = %q", got)
	}
	if _, err := os.Stat(f.l.AttemptsFile()); !os.IsNotExist(err) {
		t.Error("attempts recorded with nothing pending")
	}
}

// Each start of an unproven binary is counted; after MaxBootAttempts the
// previous binary, datastore and configuration come back, and stale WAL files
// of the abandoned datastore are not left to be replayed onto the restored one.
func TestGuardCountsStartsThenRestores(t *testing.T) {
	f := newGuardFixture(t)
	if err := os.WriteFile(f.l.PendingFile(), []byte("v1.2.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for want := 1; want <= MaxBootAttempts; want++ {
		f.run(t)
		if got := readString(t, f.l.AttemptsFile()); got != string(rune('0'+want)) {
			t.Fatalf("after start %d attempts = %q", want, got)
		}
		if got := readString(t, f.l.Binary); got != "new binary" {
			t.Fatalf("binary restored after only %d starts", want)
		}
	}

	f.run(t)
	for path, want := range map[string]string{
		f.l.Binary:      "old binary",
		f.l.Datastore(): "old db",
		f.l.ConfigFile:  "old config",
	} {
		if got := readString(t, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	for _, gone := range []string{f.l.Datastore() + "-wal", f.l.Datastore() + "-shm", f.l.PendingFile(), f.l.AttemptsFile()} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still exists", gone)
		}
	}
	if fi, err := os.Stat(f.l.Binary); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("restored binary not executable: %v", err)
	}
	if log := readString(t, f.l.GuardLog()); !strings.Contains(log, "v1.2.2 failed 3 starts; restored v1.2.1") {
		t.Errorf("guard log = %q", log)
	}

	// Once restored, later starts are left alone.
	f.run(t)
	if got := readString(t, f.l.Binary); got != "old binary" {
		t.Errorf("binary after restore = %q", got)
	}
}

func TestGuardWithIncompleteBackupRestoresNothing(t *testing.T) {
	f := newGuardFixture(t)
	_ = os.Remove(f.l.BackupDatastore())
	if err := os.WriteFile(f.l.PendingFile(), []byte("v1.2.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.l.AttemptsFile(), []byte("3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if got := readString(t, f.l.Binary); got != "new binary" {
		t.Errorf("binary = %q", got)
	}
	if log := readString(t, f.l.GuardLog()); !strings.Contains(log, "backup is incomplete") {
		t.Errorf("guard log = %q", log)
	}
}

func TestGuardTreatsGarbageAttemptsAsZero(t *testing.T) {
	f := newGuardFixture(t)
	if err := os.WriteFile(f.l.PendingFile(), []byte("v1.2.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.l.AttemptsFile(), []byte("x9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if got := readString(t, f.l.AttemptsFile()); got != "1" {
		t.Errorf("attempts = %q", got)
	}
}

// The service definitions the hook is patched into are the ones kubesoloctl and
// install.sh write. Patching twice must not add a second hook.
func TestPatchServiceDefinitions(t *testing.T) {
	l := NewLayout("/var/lib/kubesolo", "/etc/kubesolo/config.yaml")
	hook := guardLine(l)

	openrc := "#!/sbin/openrc-run\n\nname=\"kubesolo\"\ncommand=\"/usr/local/bin/kubesolo\"\n\ndepend() {\n    need net\n}\n"
	got, err := PatchOpenRC(openrc, l)
	if err != nil || !strings.Contains(got, "start_pre() {\n    "+hook+"\n}") {
		t.Errorf("openrc: %v\n%s", err, got)
	}
	if _, err := PatchOpenRC(got, l); err == nil {
		t.Error("openrc: patched a script that already has start_pre")
	}

	sysv := "#!/bin/sh\ncase \"$1\" in\n    start)\n        log_daemon_msg \"Starting\"\n        ;;\n    stop)\n        ;;\nesac\n"
	got, err = PatchSysV(sysv, l)
	if err != nil || !strings.Contains(got, "    start)\n        "+hook+"\n        log_daemon_msg") {
		t.Errorf("sysv: %v\n%s", err, got)
	}

	run := "#!/bin/sh\nexport HTTP_PROXY='x'\nexec /usr/local/bin/kubesolo '--config=/etc/kubesolo/config.yaml'\n"
	got, err = PatchRunScript(run, l)
	if err != nil || !strings.Contains(got, hook+"\nexec /usr/local/bin/kubesolo") {
		t.Errorf("run script: %v\n%s", err, got)
	}

	upstart := "description \"KubeSolo\"\nrespawn\n\nexec /usr/local/bin/kubesolo\n"
	got, err = PatchUpstart(upstart, l)
	if err != nil || !strings.Contains(got, "pre-start script\n    "+hook+"\nend script\n\nexec /usr/local/bin/kubesolo") {
		t.Errorf("upstart: %v\n%s", err, got)
	}

	for name, def := range map[string]string{"sysv": "#!/bin/sh\necho no branches\n", "run": "#!/bin/sh\n/usr/local/bin/kubesolo\n"} {
		var err error
		if name == "sysv" {
			_, err = PatchSysV(def, l)
		} else {
			_, err = PatchRunScript(def, l)
		}
		if err == nil {
			t.Errorf("%s: patched a definition with nowhere to put the hook", name)
		}
	}
}

func TestPatchDefinitionIsIdempotent(t *testing.T) {
	l := testLayout(t)
	path := filepath.Join(t.TempDir(), "run")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /usr/local/bin/kubesolo\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	patch := func(def string) (string, error) { return PatchRunScript(def, l) }
	if err := patchDefinition(path, patch); err != nil {
		t.Fatal(err)
	}
	if err := patchDefinition(path, patch); err != nil {
		t.Fatal(err)
	}
	got := readString(t, path)
	if strings.Count(got, guardMarker) != 1 {
		t.Errorf("hook added %d times:\n%s", strings.Count(got, guardMarker), got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode changed to %v", fi.Mode())
	}
}
