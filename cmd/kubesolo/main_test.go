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
