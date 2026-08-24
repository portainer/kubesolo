package kubelet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/portainer/kubesolo/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCgroupDriver(t *testing.T) {
	// The driver depends on whether systemd is the active init system; we don't
	// control that in CI, but it must always be one of the two valid values.
	assert.Contains(t, []string{"systemd", "cgroupfs"}, cgroupDriver())
}

func TestResolveCgroupDriver(t *testing.T) {
	// A driver reported by an external container runtime always wins: the kubelet
	// has to match whatever the runtime is actually using.
	s := &service{runtimeCgroupDriver: "cgroupfs"}
	assert.Equal(t, "cgroupfs", s.resolveCgroupDriver())

	s = &service{runtimeCgroupDriver: "systemd"}
	assert.Equal(t, "systemd", s.resolveCgroupDriver())

	// With nothing reported, fall back to detecting the driver from the host.
	s = &service{}
	assert.Equal(t, cgroupDriver(), s.resolveCgroupDriver())
}

func TestGenerateKubeletConfig_Common(t *testing.T) {
	s := &service{
		runtimeEndpoint: "unix:///run/kubesolo/containerd.sock",
		caFile:          "/pki/ca.crt",
		certFile:        "/pki/kubelet.crt",
		keyFile:         "/pki/kubelet.key",
		kubeletDir:      t.TempDir(),
		containerMode:   true, // avoids reading the host /etc/resolv.conf
	}
	cfg := s.generateKubeletConfig()

	assert.Equal(t, "KubeletConfiguration", cfg["kind"])
	assert.Equal(t, "kubelet.config.k8s.io/v1beta1", cfg["apiVersion"])
	assert.Equal(t, "unix:///run/kubesolo/containerd.sock", cfg["containerRuntimeEndpoint"])
	assert.Equal(t, "cluster.local", cfg["clusterDomain"])
	assert.Equal(t, []string{types.DefaultCoreDNSIP}, cfg["clusterDNS"])
	assert.Equal(t, 0, cfg["readOnlyPort"])
	assert.Equal(t, false, cfg["failSwapOn"])
	assert.Equal(t, true, cfg["rotateCertificates"])
}

func TestGenerateKubeletConfig_ContainerMode(t *testing.T) {
	s := &service{kubeletDir: t.TempDir(), containerMode: true}
	cfg := s.generateKubeletConfig()

	// In container mode the host resolv.conf is never consulted.
	assert.Equal(t, "/dev/null", cfg["resolvConf"])

	assert.Equal(t, false, cfg["cgroupsPerQOS"])
	assert.Equal(t, []string{}, cfg["enforceNodeAllocatable"])
	assert.Equal(t, 100, cfg["imageGCHighThresholdPercent"])

	evict, ok := cfg["evictionHard"].(map[string]string)
	require.True(t, ok, "evictionHard must be a string map")
	assert.Equal(t, "50Mi", evict["memory.available"])
	assert.Equal(t, "0%", evict["nodefs.available"])

	// Container mode returns early — the edge-mode keys must NOT be present.
	_, hasMaxPods := cfg["maxPods"]
	assert.False(t, hasMaxPods, "container mode must not set edge-mode maxPods")
}

func TestGenerateKubeletConfig_UpstreamDefaults(t *testing.T) {
	s := &service{kubeletDir: t.TempDir(), containerMode: false}
	cfg := s.generateKubeletConfig()

	// Outside container mode, KubeSolo uses upstream Kubernetes defaults — none of
	// the former edge overrides or the container-mode QoS keys are set.
	for _, k := range []string{"maxPods", "enableProfilingHandler", "imageGCHighThresholdPercent", "evictionHard", "systemReserved", "kubeReserved", "cgroupsPerQOS"} {
		_, ok := cfg[k]
		assert.Falsef(t, ok, "upstream defaults must not set %q", k)
	}

	// Baseline secure-default keys still apply.
	assert.Equal(t, 0, cfg["readOnlyPort"])
	assert.Equal(t, false, cfg["failSwapOn"])
	assert.Equal(t, true, cfg["rotateCertificates"])
}

func TestGenerateKubeletConfig_CPUManagerDisabled(t *testing.T) {
	s := &service{kubeletDir: t.TempDir(), cpuManager: types.CPUManagerConfig{Policy: types.CPUManagerPolicyNone}}
	cfg := s.generateKubeletConfig()

	// Existing installs must not gain CPU manager keys.
	for _, k := range []string{"cpuManagerPolicy", "cpuManagerPolicyOptions", "reservedSystemCPUs"} {
		_, ok := cfg[k]
		assert.Falsef(t, ok, "the none policy must not set %q", k)
	}
}

func TestGenerateKubeletConfig_CPUManagerStatic(t *testing.T) {
	s := &service{
		kubeletDir: t.TempDir(),
		cpuManager: types.CPUManagerConfig{
			Policy:        types.CPUManagerPolicyStatic,
			PolicyOptions: map[string]string{"full-pcpus-only": "true"},
			ReservedCPUs:  "0-1",
		},
	}
	cfg := s.generateKubeletConfig()

	assert.Equal(t, "static", cfg["cpuManagerPolicy"])
	assert.Equal(t, "0-1", cfg["reservedSystemCPUs"])
	assert.Equal(t, map[string]string{"full-pcpus-only": "true"}, cfg["cpuManagerPolicyOptions"])
}

func TestGenerateKubeletConfig_CPUManagerStaticWithoutOptions(t *testing.T) {
	s := &service{
		kubeletDir: t.TempDir(),
		cpuManager: types.CPUManagerConfig{Policy: types.CPUManagerPolicyStatic, ReservedCPUs: "0"},
	}
	cfg := s.generateKubeletConfig()

	assert.Equal(t, "static", cfg["cpuManagerPolicy"])
	_, ok := cfg["cpuManagerPolicyOptions"]
	assert.False(t, ok, "an empty option set must be omitted rather than written as an empty map")
}

// writeConfigWith writes a kubelet config for the given CPU manager settings into dir
// and returns the service, so a following write can be tested against it.
func writeConfigWith(t *testing.T, dir string, cpuManager types.CPUManagerConfig) *service {
	t.Helper()

	s := &service{
		kubeletDir:        dir,
		kubeletConfigDir:  filepath.Join(dir, "config"),
		kubeletConfigFile: filepath.Join(dir, "config", "config.yaml"),
		containerMode:     true, // avoids reading the host /etc/resolv.conf
		cpuManager:        cpuManager,
	}
	require.NoError(t, s.writeKubeletConfigFile())

	return s
}

func TestInvalidateCPUManagerCheckpoint(t *testing.T) {
	static := types.CPUManagerConfig{Policy: types.CPUManagerPolicyStatic, ReservedCPUs: "0"}

	tests := []struct {
		name     string
		first    types.CPUManagerConfig
		second   types.CPUManagerConfig
		survives bool
	}{
		{
			name:     "settings unchanged",
			first:    static,
			second:   static,
			survives: true,
		},
		{
			name:     "no cpu manager on either start",
			first:    types.CPUManagerConfig{Policy: types.CPUManagerPolicyNone},
			second:   types.CPUManagerConfig{Policy: types.CPUManagerPolicyNone},
			survives: true,
		},
		{
			name:   "policy enabled",
			first:  types.CPUManagerConfig{Policy: types.CPUManagerPolicyNone},
			second: static,
		},
		{
			name:   "policy disabled",
			first:  static,
			second: types.CPUManagerConfig{Policy: types.CPUManagerPolicyNone},
		},
		{
			name:   "reservation changed",
			first:  static,
			second: types.CPUManagerConfig{Policy: types.CPUManagerPolicyStatic, ReservedCPUs: "0-1"},
		},
		{
			name:   "options changed",
			first:  static,
			second: types.CPUManagerConfig{Policy: types.CPUManagerPolicyStatic, ReservedCPUs: "0", PolicyOptions: map[string]string{"full-pcpus-only": "true"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfigWith(t, dir, tt.first)

			checkpoint := filepath.Join(dir, cpuManagerCheckpointFile)
			require.NoError(t, os.WriteFile(checkpoint, []byte(`{"policyName":"static"}`), 0o600))

			writeConfigWith(t, dir, tt.second)

			_, err := os.Stat(checkpoint)
			if tt.survives {
				assert.NoError(t, err, "checkpoint must be kept when the settings are unchanged")
				return
			}
			assert.True(t, os.IsNotExist(err), "checkpoint must be removed when the settings change")
		})
	}
}

func TestInvalidateCPUManagerCheckpoint_FirstStart(t *testing.T) {
	// With no previous config there is nothing to compare against, and no checkpoint
	// to remove. This must not error.
	dir := t.TempDir()
	writeConfigWith(t, dir, types.CPUManagerConfig{Policy: types.CPUManagerPolicyStatic, ReservedCPUs: "0"})

	_, err := os.Stat(filepath.Join(dir, "config", "config.yaml"))
	assert.NoError(t, err)
}

func TestGenerateKubeletConfig_SystemReserved(t *testing.T) {
	s := &service{
		kubeletDir:     t.TempDir(),
		systemReserved: map[string]string{"cpu": "1", "memory": "500Mi"},
	}
	cfg := s.generateKubeletConfig()

	assert.Equal(t, map[string]string{"cpu": "1", "memory": "500Mi"}, cfg["systemReserved"])
}

func TestGenerateKubeletConfig_SystemReservedOmittedWhenUnset(t *testing.T) {
	s := &service{kubeletDir: t.TempDir()}
	cfg := s.generateKubeletConfig()

	_, ok := cfg["systemReserved"]
	assert.False(t, ok, "an empty reservation must be omitted rather than written as an empty map")
}

func TestGenerateKubeletConfig_ContainerModeKeepsSystemReserved(t *testing.T) {
	// Container mode blanks systemReserved by default, but must not discard a
	// reservation the operator asked for.
	s := &service{
		kubeletDir:     t.TempDir(),
		containerMode:  true,
		systemReserved: map[string]string{"memory": "500Mi"},
	}
	cfg := s.generateKubeletConfig()

	assert.Equal(t, map[string]string{"memory": "500Mi"}, cfg["systemReserved"])
	assert.Equal(t, map[string]string{}, cfg["kubeReserved"])
}
