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
		cfg, err := Parse(policy, "", "", 4)
		require.NoError(t, err)
		assert.Equal(t, types.CPUManagerPolicyNone, cfg.Policy)
		assert.Empty(t, cfg.PolicyOptions)
		assert.Empty(t, cfg.ReservedCPUs)
	}
}

func TestParse_StaticDefaultsReservation(t *testing.T) {
	// The static policy refuses to start without a reservation, so one is applied.
	cfg, err := Parse(types.CPUManagerPolicyStatic, "", "", 4)
	require.NoError(t, err)
	assert.Equal(t, types.CPUManagerPolicyStatic, cfg.Policy)
	assert.Equal(t, "0", cfg.ReservedCPUs)
}

func TestParse_StaticExplicitReservation(t *testing.T) {
	cfg, err := Parse(types.CPUManagerPolicyStatic, "", "0-1", 4)
	require.NoError(t, err)
	assert.Equal(t, "0-1", cfg.ReservedCPUs)
}

func TestParse_NormalisesOptionValues(t *testing.T) {
	// "1" and "true" mean the same thing to the kubelet, but only a canonical value
	// keeps the generated config stable across restarts.
	cfg, err := Parse(types.CPUManagerPolicyStatic, "full-pcpus-only=1, strict-cpu-reservation=true", "0", 4)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"full-pcpus-only":        "true",
		"strict-cpu-reservation": "true",
	}, cfg.PolicyOptions)
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name         string
		policy       string
		options      string
		reservedCPUs string
		numCPU       int
	}{
		{"unknown policy", "dynamic", "", "", 4},
		{"options without static", types.CPUManagerPolicyNone, "full-pcpus-only=true", "", 4},
		{"reserved cpus without static", types.CPUManagerPolicyNone, "", "0", 4},
		{"alpha option", types.CPUManagerPolicyStatic, "align-by-socket=true", "0", 4},
		{"unknown option", types.CPUManagerPolicyStatic, "make-it-fast=true", "0", 4},
		{"option without value", types.CPUManagerPolicyStatic, "full-pcpus-only", "0", 4},
		{"non-boolean option value", types.CPUManagerPolicyStatic, "full-pcpus-only=yes-please", "0", 4},
		{"incompatible options", types.CPUManagerPolicyStatic, "prefer-align-cpus-by-uncorecache=true,distribute-cpus-across-numa=true", "0", 4},
		{"unparseable cpuset", types.CPUManagerPolicyStatic, "", "zero", 4},
		{"reserved cpu does not exist", types.CPUManagerPolicyStatic, "", "9", 4},
		{"every cpu reserved", types.CPUManagerPolicyStatic, "", "0-3", 4},
		{"single cpu host", types.CPUManagerPolicyStatic, "", "", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.policy, tt.options, tt.reservedCPUs, tt.numCPU)
			assert.Error(t, err)
		})
	}
}
