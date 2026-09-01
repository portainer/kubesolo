package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	want := Defaults()
	want.Network.NodeIP = "10.0.0.5"
	want.Portainer.EdgeKey = "secret"
	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}

	got := Defaults()
	found, warnings, err := Read(path, got)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Read reported the file as absent")
	}
	if len(warnings) != 0 {
		t.Errorf("a file we wrote ourselves should read back cleanly, got %v", warnings)
	}
	if got.Network.NodeIP != "10.0.0.5" || got.Portainer.EdgeKey != "secret" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// TestWriteIsRootReadableOnly matters because the document carries
// portainer.edgeKey.
func TestWriteIsRootReadableOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Write(path, Defaults()); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != configFileMode {
		t.Errorf("mode = %o, want %o", got, configFileMode)
	}
}

func TestWriteCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etc", "kubesolo", "config.yaml")
	if err := Write(path, Defaults()); err != nil {
		t.Fatalf("Write should create the directory: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// TestWriteDoesNotUseSystemTempDir is a proxy for the EXDEV hazard. The
// temporary file must be created beside the target, because /etc and /tmp are
// routinely separate filesystems and os.Rename cannot cross one. Pointing TMPDIR
// at a path that does not exist fails any implementation that reached for the
// system temp directory instead.
func TestWriteDoesNotUseSystemTempDir(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Write(path, Defaults()); err != nil {
		t.Fatalf("Write must not depend on the system temp directory: %v", err)
	}
}

func TestWriteLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	for i := 0; i < 3; i++ {
		if err := Write(path, Defaults()); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestWriteBacksUpPreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	first := Defaults()
	first.Network.NodeIP = "10.0.0.1"
	if err := Write(path, first); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + backupSuffix); !os.IsNotExist(err) {
		t.Error("the first write has nothing to back up")
	}

	second := Defaults()
	second.Network.NodeIP = "10.0.0.2"
	if err := Write(path, second); err != nil {
		t.Fatal(err)
	}

	backup := Defaults()
	if _, _, err := Read(path+backupSuffix, backup); err != nil {
		t.Fatal(err)
	}
	if backup.Network.NodeIP != "10.0.0.1" {
		t.Errorf("backup holds %q, want the previous value 10.0.0.1", backup.Network.NodeIP)
	}

	current := Defaults()
	if _, _, err := Read(path, current); err != nil {
		t.Fatal(err)
	}
	if current.Network.NodeIP != "10.0.0.2" {
		t.Errorf("config holds %q, want the new value 10.0.0.2", current.Network.NodeIP)
	}
}

func TestBackupIsRootReadableOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Write(path, Defaults()); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, Defaults()); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path + backupSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != configFileMode {
		t.Errorf("backup mode = %o, want %o; it carries the same credentials", got, configFileMode)
	}
}

// TestFailedWriteLeavesOriginalIntact is the crash-safety property: a write that
// cannot complete must not damage the configuration already in place.
func TestFailedWriteLeavesOriginalIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which ignores the directory permissions this test relies on")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := Defaults()
	original.Network.NodeIP = "10.0.0.1"
	if err := Write(path, original); err != nil {
		t.Fatal(err)
	}

	// Read-only directory: the temporary file cannot be created, so the write
	// fails before it can touch the existing file.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	replacement := Defaults()
	replacement.Network.NodeIP = "10.0.0.2"
	if err := Write(path, replacement); err == nil {
		t.Fatal("expected the write to fail")
	}

	survivor := Defaults()
	if _, _, err := Read(path, survivor); err != nil {
		t.Fatal(err)
	}
	if survivor.Network.NodeIP != "10.0.0.1" {
		t.Errorf("the original was damaged: nodeIP = %q, want 10.0.0.1", survivor.Network.NodeIP)
	}
}

// TestWriteFillsSchemaFields guards against producing a document that reads back
// with a warning, which an API PUT of a bare body would otherwise do.
func TestWriteFillsSchemaFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	cfg := Defaults()
	cfg.APIVersion = ""
	cfg.Kind = ""
	if err := Write(path, cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.APIVersion != "" {
		t.Error("Write must not mutate the caller's config")
	}

	_, warnings, err := Read(path, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("written file should declare its schema, got %v", warnings)
	}
}

// TestUnexpectedKindWarns covers a document copied from another tool: the schema
// is not in doubt when the apiVersion is right, so the file is still read — but
// accepting a foreign kind in silence hides the mistake.
func TestUnexpectedKindWarns(t *testing.T) {
	for _, tc := range []struct {
		name        string
		kind        string
		wantWarning bool
	}{
		{"correct kind", "kind: Config\n", false},
		{"absent kind", "", false},
		{"foreign kind", "kind: ClusterConfiguration\n", true},
		{"misspelled kind", "kind: config\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := "apiVersion: kubesolo.io/v1alpha1\n" + tc.kind + "network:\n  nodeIP: 10.0.0.5\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg := Defaults()
			_, warnings, err := Read(path, cfg)
			if err != nil {
				t.Fatalf("a kind mismatch must not be fatal: %v", err)
			}
			if cfg.Network.NodeIP != "10.0.0.5" {
				t.Errorf("the file should still be read, nodeIP = %q", cfg.Network.NodeIP)
			}

			got := false
			for _, w := range warnings {
				if strings.Contains(w.Message, "kind") {
					got = true
				}
			}
			if got != tc.wantWarning {
				t.Errorf("kind warning = %v, want %v (warnings: %v)", got, tc.wantWarning, warnings)
			}
		})
	}
}
