// Package cpumanager validates the kubelet CPU manager flags so a bad combination
// fails at startup with a clear message, rather than crash-looping the kubelet.
package cpumanager

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/portainer/kubesolo/types"
	"k8s.io/utils/cpuset"
)

const (
	optionFullPCPUsOnly        = "full-pcpus-only"
	optionStrictCPUReservation = "strict-cpu-reservation"
	optionDistributeAcrossNUMA = "distribute-cpus-across-numa"
	optionPreferUncoreCache    = "prefer-align-cpus-by-uncorecache"

	// defaultReservedCPUs is applied when the static policy is requested without
	// --reserved-cpus. The static policy refuses to start without a non-zero CPU
	// reservation, and naming CPU 0 is more predictable than a kubeReserved count
	// that lets the kubelet choose which CPU to hold back.
	defaultReservedCPUs = "0"
)

// supportedOptions are the stable and beta policy options on Kubernetes 1.35, which
// need no feature gates. The two alpha options (align-by-socket and
// distribute-cpus-across-cores) are deliberately absent: they require
// CPUManagerPolicyAlphaOptions and neither helps on the single-socket hardware
// kubesolo targets.
var supportedOptions = []string{
	optionFullPCPUsOnly,
	optionStrictCPUReservation,
	optionDistributeAcrossNUMA,
	optionPreferUncoreCache,
}

// Parse validates the CPU manager flags against a host with numCPU CPUs and returns
// the resulting configuration.
func Parse(policy, options, reservedCPUs string, numCPU int) (types.CPUManagerConfig, error) {
	switch policy {
	case "", types.CPUManagerPolicyNone:
		if options != "" || reservedCPUs != "" {
			return types.CPUManagerConfig{}, fmt.Errorf("--cpu-manager-policy-options and --reserved-cpus require --cpu-manager-policy=%s", types.CPUManagerPolicyStatic)
		}
		return types.CPUManagerConfig{Policy: types.CPUManagerPolicyNone}, nil
	case types.CPUManagerPolicyStatic:
	default:
		return types.CPUManagerConfig{}, fmt.Errorf("invalid --cpu-manager-policy %q: must be %s or %s", policy, types.CPUManagerPolicyNone, types.CPUManagerPolicyStatic)
	}

	parsedOptions, err := parseOptions(options)
	if err != nil {
		return types.CPUManagerConfig{}, err
	}

	if reservedCPUs == "" {
		reservedCPUs = defaultReservedCPUs
	}

	reserved, err := cpuset.Parse(reservedCPUs)
	if err != nil {
		return types.CPUManagerConfig{}, fmt.Errorf("invalid --reserved-cpus %q: %v", reservedCPUs, err)
	}

	for _, cpu := range reserved.List() {
		if cpu >= numCPU {
			return types.CPUManagerConfig{}, fmt.Errorf("invalid --reserved-cpus %q: CPU %d does not exist, this host has %d CPUs", reservedCPUs, cpu, numCPU)
		}
	}

	if numCPU-reserved.Size() < 1 {
		return types.CPUManagerConfig{}, fmt.Errorf("--reserved-cpus %q reserves all %d CPUs, leaving none to pin workloads to", reservedCPUs, numCPU)
	}

	return types.CPUManagerConfig{
		Policy:        types.CPUManagerPolicyStatic,
		PolicyOptions: parsedOptions,
		ReservedCPUs:  reservedCPUs,
	}, nil
}

// parseOptions turns a comma-separated key=value list into the map the kubelet
// config expects, normalising each value so "1" and "true" produce the same config.
func parseOptions(options string) (map[string]string, error) {
	if options == "" {
		return nil, nil
	}

	parsed := make(map[string]string)
	for _, option := range strings.Split(options, ",") {
		name, value, found := strings.Cut(option, "=")
		if !found {
			return nil, fmt.Errorf("invalid --cpu-manager-policy-options entry %q: expected key=value", option)
		}

		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !slices.Contains(supportedOptions, name) {
			return nil, fmt.Errorf("unsupported --cpu-manager-policy-options key %q: supported keys are %s", name, strings.Join(supportedOptions, ", "))
		}

		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid value %q for --cpu-manager-policy-options key %q: expected a boolean", value, name)
		}
		parsed[name] = strconv.FormatBool(enabled)
	}

	// Mirrors the kubelet's own check so the error surfaces at startup rather than
	// after the kubelet has already been launched.
	if parsed[optionPreferUncoreCache] == "true" && parsed[optionDistributeAcrossNUMA] == "true" {
		return nil, fmt.Errorf("--cpu-manager-policy-options %s and %s cannot both be enabled", optionPreferUncoreCache, optionDistributeAcrossNUMA)
	}

	return parsed, nil
}
