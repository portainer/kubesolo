package containerd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedRuntimeTypeLaunchesEmbeddedShim(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "containerd-shim-runc-v2")
	marker := filepath.Join(dir, "launched")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nprintf launched > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := &service{
		containerdRootDir:        t.TempDir(),
		containerdShimBinaryFile: shim,
	}
	config := s.generateContainerdConfig()
	plugins := config["plugins"].(map[string]any)
	cri := plugins["io.containerd.cri.v1.runtime"].(map[string]any)
	containerdConfig := cri["containerd"].(map[string]any)
	runtimes := containerdConfig["runtimes"].(map[string]any)
	crun := runtimes["crun"].(map[string]any)
	runtimeType := crun["runtime_type"].(string)

	if runtimeType != shim || !filepath.IsAbs(runtimeType) {
		t.Fatalf("runtime_type = %q, want absolute embedded shim path %q", runtimeType, shim)
	}
	if err := exec.Command(runtimeType, marker).Run(); err != nil {
		t.Fatalf("configured shim did not launch: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("configured shim did not run: %v", err)
	}
}
