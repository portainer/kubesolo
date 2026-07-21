package containerd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/plugins"
)

func TestGeneratedRuntimeUsesRegisteredTypeAndEmbeddedShimPath(t *testing.T) {
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
	plugins_ := config["plugins"].(map[string]any)
	cri := plugins_["io.containerd.cri.v1.runtime"].(map[string]any)
	containerdConfig := cri["containerd"].(map[string]any)
	runtimes := containerdConfig["runtimes"].(map[string]any)
	crun := runtimes["crun"].(map[string]any)

	// runtime_type must remain the registered runc-v2 type. CRI keys off this
	// exact string to select the shim options message; anything else falls back
	// to runtimeoptions.v1.Options, which the runc-v2 shim cannot decode.
	runtimeType := crun["runtime_type"].(string)
	if runtimeType != plugins.RuntimeRuncV2 {
		t.Fatalf("runtime_type = %q, want %q", runtimeType, plugins.RuntimeRuncV2)
	}

	// runtime_path points containerd at the extracted shim (must be absolute)
	// so no symlink is installed under /usr/bin.
	runtimePath := crun["runtime_path"].(string)
	if runtimePath != shim || !filepath.IsAbs(runtimePath) {
		t.Fatalf("runtime_path = %q, want absolute embedded shim path %q", runtimePath, shim)
	}
	if err := exec.Command(runtimePath, marker).Run(); err != nil {
		t.Fatalf("configured shim did not launch: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("configured shim did not run: %v", err)
	}
}
