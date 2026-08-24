package cpumanager

import (
	"testing"

	"github.com/portainer/kubesolo/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Defaults(t *testing.T) {
	// The shipped default, and the empty policy an older config might carry, must both
	// leave the CPU manager off so existing installs are unaffected.
	for _, policy := range []string{types.CPUManagerPolicyNone, ""} {
		cfg, _, err := Parse(policy, "", "", "", 4)
		require.NoError(t, err)
		assert.Equal(t, types.CPUManagerPolicyNone, cfg.Policy)
		assert.Empty(t, cfg.PolicyOptions)
		assert.Empty(t, cfg.ReservedCPUs)
	}
}

func TestParse_StaticDefaultsReservation(t *testing.T) {
	// The static policy refuses to start without a reservation, so one is applied.
	cfg, _, err := Parse(types.CPUManagerPolicyStatic, "", "", "", 4)
	require.NoError(t, err)
	assert.Equal(t, types.CPUManagerPolicyStatic, cfg.Policy)
	assert.Equal(t, "0", cfg.ReservedCPUs)
}

func TestParse_StaticExplicitReservation(t *testing.T) {
	cfg, _, err := Parse(types.CPUManagerPolicyStatic, "", "0-1", "", 4)
	require.NoError(t, err)
	assert.Equal(t, "0-1", cfg.ReservedCPUs)
}

func TestParse_NormalisesOptionValues(t *testing.T) {
	// "1" and "true" mean the same thing to the kubelet, but only a canonical value
	// keeps the generated config stable across restarts.
	cfg, _, err := Parse(types.CPUManagerPolicyStatic, "full-pcpus-only=1, strict-cpu-reservation=true", "0", "", 4)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"full-pcpus-only":        "true",
		"strict-cpu-reservation": "true",
	}, cfg.PolicyOptions)
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name           string
		policy         string
		options        string
		reservedCPUs   string
		systemReserved string
		numCPU         int
	}{
		{"unknown policy", "dynamic", "", "", "", 4},
		{"options without static", types.CPUManagerPolicyNone, "full-pcpus-only=true", "", "", 4},
		{"reserved cpus without static", types.CPUManagerPolicyNone, "", "0", "", 4},
		{"alpha option", types.CPUManagerPolicyStatic, "align-by-socket=true", "0", "", 4},
		{"unknown option", types.CPUManagerPolicyStatic, "make-it-fast=true", "0", "", 4},
		{"option without value", types.CPUManagerPolicyStatic, "full-pcpus-only", "0", "", 4},
		{"non-boolean option value", types.CPUManagerPolicyStatic, "full-pcpus-only=yes-please", "0", "", 4},
		{"incompatible options", types.CPUManagerPolicyStatic, "prefer-align-cpus-by-uncorecache=true,distribute-cpus-across-numa=true", "0", "", 4},
		{"unparseable cpuset", types.CPUManagerPolicyStatic, "", "zero", "", 4},
		{"reserved cpu does not exist", types.CPUManagerPolicyStatic, "", "9", "", 4},
		{"every cpu reserved", types.CPUManagerPolicyStatic, "", "0-3", "", 4},
		{"single cpu host", types.CPUManagerPolicyStatic, "", "", "", 1},
		{"system-reserved without key=value", types.CPUManagerPolicyNone, "", "", "cpu", 4},
		{"system-reserved unknown resource", types.CPUManagerPolicyNone, "", "", "gpu=1", 4},
		{"system-reserved bad quantity", types.CPUManagerPolicyNone, "", "", "memory=lots", 4},
		{"system-reserved reserves every cpu", types.CPUManagerPolicyStatic, "", "", "cpu=4", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse(tt.policy, tt.options, tt.reservedCPUs, tt.systemReserved, tt.numCPU)
			assert.Error(t, err)
		})
	}
}

func TestParse_SystemReserved(t *testing.T) {
	// A reservation expressed as a count satisfies the static policy without naming
	// cores, so no cpuset is emitted and the kubelet picks them.
	cfg, reserved, err := Parse(types.CPUManagerPolicyStatic, "", "", "cpu=1,memory=500Mi", 4)
	require.NoError(t, err)
	assert.Equal(t, types.CPUManagerPolicyStatic, cfg.Policy)
	assert.Empty(t, cfg.ReservedCPUs, "a count-based reservation must not synthesise a cpuset")
	assert.Equal(t, map[string]string{"cpu": "1", "memory": "500Mi"}, reserved)
}

func TestParse_SystemReservedWithoutCPUStillDefaultsCpuset(t *testing.T) {
	// Reserving only memory leaves the static policy with no CPU reservation, so the
	// cpuset default still has to apply or the kubelet refuses to start.
	cfg, reserved, err := Parse(types.CPUManagerPolicyStatic, "", "", "memory=500Mi", 4)
	require.NoError(t, err)
	assert.Equal(t, "0", cfg.ReservedCPUs)
	assert.Equal(t, map[string]string{"memory": "500Mi"}, reserved)
}

func TestParse_SystemReservedAllowedWithoutCPUManager(t *testing.T) {
	// It is a general node-allocatable knob, so it stays valid with the policy off.
	cfg, reserved, err := Parse(types.CPUManagerPolicyNone, "", "", "memory=500Mi,pid=1000", 4)
	require.NoError(t, err)
	assert.Equal(t, types.CPUManagerPolicyNone, cfg.Policy)
	assert.Equal(t, map[string]string{"memory": "500Mi", "pid": "1000"}, reserved)
}

func TestParse_ReservedCPUsTakesPrecedenceOverSystemReservedCPU(t *testing.T) {
	// Upstream defines the cpuset as overriding the cpu entry, so the pair is valid.
	// Both are passed through and the kubelet applies the documented precedence.
	cfg, reserved, err := Parse(types.CPUManagerPolicyStatic, "", "0-1", "cpu=2,memory=500Mi", 4)
	require.NoError(t, err)
	assert.Equal(t, "0-1", cfg.ReservedCPUs)
	assert.Equal(t, map[string]string{"cpu": "2", "memory": "500Mi"}, reserved)
}

func TestParse_OversizedSystemReservedCPUIgnoredWhenOverridden(t *testing.T) {
	// A cpu entry that could never fit is harmless once a cpuset overrides it, and
	// the kubelet validates the overridden value rather than this one.
	cfg, _, err := Parse(types.CPUManagerPolicyStatic, "", "0", "cpu=16", 4)
	require.NoError(t, err)
	assert.Equal(t, "0", cfg.ReservedCPUs)
}
