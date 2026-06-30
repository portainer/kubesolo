package kubelet

import (
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

func TestGenerateKubeletConfig_Common(t *testing.T) {
	s := &service{
		containerdSockFile: "/run/kubesolo/containerd.sock",
		caFile:             "/pki/ca.crt",
		certFile:           "/pki/kubelet.crt",
		keyFile:            "/pki/kubelet.key",
		kubeletDir:         t.TempDir(),
		containerMode:      true, // avoids reading the host /etc/resolv.conf
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
	s := &service{kubeletDir: t.TempDir(), containerMode: true, fullMode: false}
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

func TestGenerateKubeletConfig_EdgeMode(t *testing.T) {
	s := &service{kubeletDir: t.TempDir(), containerMode: false, fullMode: false}
	cfg := s.generateKubeletConfig()

	assert.Equal(t, 20, cfg["maxPods"])
	assert.Equal(t, false, cfg["enableProfilingHandler"])
	assert.Equal(t, 95, cfg["imageGCHighThresholdPercent"])

	evict, ok := cfg["evictionHard"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "75Mi", evict["memory.available"])

	sysReserved, ok := cfg["systemReserved"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "25Mi", sysReserved["memory"])

	// Edge mode is not container mode — the QoS-disabling key must be absent.
	_, hasCgroupsPerQOS := cfg["cgroupsPerQOS"]
	assert.False(t, hasCgroupsPerQOS)
}

func TestGenerateKubeletConfig_FullMode(t *testing.T) {
	s := &service{kubeletDir: t.TempDir(), containerMode: false, fullMode: true}
	cfg := s.generateKubeletConfig()

	// Full mode uses upstream defaults — neither edge nor container overrides apply.
	for _, k := range []string{"maxPods", "enableProfilingHandler", "cgroupsPerQOS", "evictionHard"} {
		_, ok := cfg[k]
		assert.Falsef(t, ok, "full mode must not set %q", k)
	}

	// Baseline secure-default keys still apply in full mode.
	assert.Equal(t, 0, cfg["readOnlyPort"])
	assert.Equal(t, false, cfg["failSwapOn"])
}
