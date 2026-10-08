package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh install has no marker and no data directory yet. Recording the boot
// has to work anyway, or the first restart in that boot is mistaken for a
// reboot and clears task state out from under running shims.
func TestRebootedSinceLastRun_FreshInstall(t *testing.T) {
	if readBootID() == "" {
		t.Skip("no kernel boot_id on this host")
	}
	basePath := filepath.Join(t.TempDir(), "kubesolo")

	if !rebootedSinceLastRun(basePath) {
		t.Error("first run with no marker: got false, want true")
	}

	marker := filepath.Join(basePath, bootIDMarkerFile)
	recorded, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker not written to a directory that did not exist: %v", err)
	}
	if string(recorded) != readBootID() {
		t.Errorf("marker = %q, want the current boot id %q", recorded, readBootID())
	}

	if rebootedSinceLastRun(basePath) {
		t.Error("second run in the same boot: got true, want false")
	}
}

func TestRebootedSinceLastRun_MarkerFromAnotherBoot(t *testing.T) {
	if readBootID() == "" {
		t.Skip("no kernel boot_id on this host")
	}
	basePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(basePath, bootIDMarkerFile), []byte("a-previous-boot"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !rebootedSinceLastRun(basePath) {
		t.Error("marker from another boot: got false, want true")
	}
}

// writeProc fakes /proc/<pid>/cmdline with NUL-separated arguments.
func writeProc(t *testing.T, root, pid string, args ...string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdline := ""
	for _, a := range args {
		cmdline += a + "\x00"
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The first start after an upgrade from a release without a boot marker looks
// like a reboot. Live shims of this containerd prove it is not, and must be
// counted; shims of any other containerd must not.
func TestLiveShims(t *testing.T) {
	root := t.TempDir()
	socket := "/var/lib/kubesolo/containerd/containerd.sock"

	writeProc(t, root, "100", "/var/lib/kubesolo/containerd/containerd-shim-runc-v2", "-namespace", "k8s.io", "-id", "abc", "-address", socket)
	writeProc(t, root, "101", "/var/lib/kubesolo/containerd/containerd-shim-runc-v2", "-namespace", "k8s.io", "-id", "def", "-address", socket)
	writeProc(t, root, "200", "/usr/bin/containerd-shim-runc-v2", "-namespace", "moby", "-id", "x", "-address", "/run/containerd/containerd.sock")
	writeProc(t, root, "300", "nginx: master process nginx")
	writeProc(t, root, "301", "/bin/sh", "-c", "echo -address "+socket)
	writeProc(t, root, "self", "/var/lib/kubesolo/containerd/containerd-shim-runc-v2", "-address", socket)
	if err := os.MkdirAll(filepath.Join(root, "400"), 0o755); err != nil { // exited: no cmdline
		t.Fatal(err)
	}

	if got := liveShims(root, socket); got != 2 {
		t.Errorf("liveShims = %d, want 2", got)
	}
	if got := liveShims(root, "/elsewhere/containerd.sock"); got != 0 {
		t.Errorf("liveShims for another socket = %d, want 0", got)
	}
	if got := liveShims(filepath.Join(root, "missing"), socket); got != 0 {
		t.Errorf("liveShims with no proc = %d, want 0", got)
	}
}
