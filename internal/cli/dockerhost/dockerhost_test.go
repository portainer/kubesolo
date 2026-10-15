package dockerhost

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeContext(t *testing.T, dir, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	meta := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"Name":"` + name + `","Metadata":{},"Endpoints":{"docker":{"Host":"` + host + `","SkipTLSVerify":false}}}`
	if err := os.WriteFile(filepath.Join(meta, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T, current string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	if current != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{},"currentContext":"`+current+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeContext(t, dir, "colima", "unix:///Users/u/.colima/default/docker.sock")
	writeContext(t, dir, "other", "unix:///run/other.sock")
	return dir
}

func TestHost(t *testing.T) {
	t.Run("no context: client defaults", func(t *testing.T) {
		setup(t, "")
		if h, err := Host(); err != nil || h != "" {
			t.Errorf("%q, %v", h, err)
		}
	})
	t.Run("default context: client defaults", func(t *testing.T) {
		setup(t, "default")
		if h, err := Host(); err != nil || h != "" {
			t.Errorf("%q, %v", h, err)
		}
	})
	t.Run("current context", func(t *testing.T) {
		setup(t, "colima")
		if h, err := Host(); err != nil || h != "unix:///Users/u/.colima/default/docker.sock" {
			t.Errorf("%q, %v", h, err)
		}
	})
	t.Run("DOCKER_CONTEXT wins over the config", func(t *testing.T) {
		setup(t, "colima")
		t.Setenv("DOCKER_CONTEXT", "other")
		if h, err := Host(); err != nil || h != "unix:///run/other.sock" {
			t.Errorf("%q, %v", h, err)
		}
	})
	t.Run("DOCKER_HOST wins over any context", func(t *testing.T) {
		setup(t, "colima")
		t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
		if h, err := Host(); err != nil || h != "unix:///var/run/docker.sock" {
			t.Errorf("%q, %v", h, err)
		}
	})
	t.Run("unknown context", func(t *testing.T) {
		setup(t, "missing")
		if _, err := Host(); err == nil {
			t.Error("expected an error")
		}
	})
}
