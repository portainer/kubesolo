package containerd

import (
	"testing"

	"github.com/containerd/containerd/v2/plugins"
)

func TestGeneratedRuntimeUsesRegisteredTypeWithoutRuntimePath(t *testing.T) {
	s := &service{
		containerdRootDir:        t.TempDir(),
		containerdShimBinaryFile: "/var/lib/kubesolo/containerd/containerd-shim-runc-v2",
		crunBinaryFile:           "/var/lib/kubesolo/containerd/crun",
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

	// runtime_path must NOT be set. An absolute runtime_path makes containerd use
	// the path as the runtime identifier, which bypasses the built-in short-circuit
	// for io.containerd.runc.v2 and triggers a spurious `<shim> -info` probe that
	// defaults to looking up "runc" (not shipped). The shim is resolved from $PATH
	// instead (executor.go prepends the containerd binary dir).
	if v, ok := crun["runtime_path"]; ok {
		t.Fatalf("runtime_path must not be set; got %q", v)
	}

	// crun stays the OCI runtime via options.BinaryName.
	options := crun["options"].(map[string]any)
	if got := options["BinaryName"].(string); got != s.crunBinaryFile {
		t.Fatalf("options.BinaryName = %q, want %q", got, s.crunBinaryFile)
	}
}
