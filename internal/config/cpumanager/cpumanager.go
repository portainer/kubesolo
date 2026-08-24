// Package cpumanager validates the kubelet CPU-manager and node-reservation flags so
// a bad combination fails at startup with a clear message, rather than crash-looping
// the kubelet.
package cpumanager

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/cpuset"
)

const (
	optionFullPCPUsOnly        = "full-pcpus-only"
	optionStrictCPUReservation = "strict-cpu-reservation"
	optionDistributeAcrossNUMA = "distribute-cpus-across-numa"
	optionPreferUncoreCache    = "prefer-align-cpus-by-uncorecache"

	resourceCPU = "cpu"

	// defaultReservedCPUs is applied when the static policy is requested without any
	// CPU reservation at all. The static policy refuses to start without one, and
	// naming CPU 0 is more predictable than a count that lets the kubelet choose
	// which CPU to hold back.
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

// systemReservedResources are the resources the kubelet accepts in --system-reserved.
var systemReservedResources = []string{resourceCPU, "memory", "ephemeral-storage", "pid"}

// Parse validates the CPU-manager and node-reservation flags against a host with
// numCPU CPUs, returning the CPU manager configuration and the system reservation.
func Parse(policy, options, reservedCPUs, systemReserved string, numCPU int) (types.CPUManagerConfig, map[string]string, error) {
	reserved, err := parseSystemReserved(systemReserved)
	if err != nil {
		return types.CPUManagerConfig{}, nil, err
	}
	reservedCPUQuantity, hasReservedCPU := reserved[resourceCPU]

	// An explicit cpuset overrides the cpu entry, so only guard the count when it is
	// the reservation that actually takes effect.
	if reservedCPUs == "" && hasReservedCPU {
		if err := checkReservedCPUCount(reservedCPUQuantity, numCPU); err != nil {
			return types.CPUManagerConfig{}, nil, err
		}
	}

	switch policy {
	case "", types.CPUManagerPolicyNone:
		// --system-reserved is a general node-allocatable knob and stays valid here;
		// the CPU-manager flags do not.
		if options != "" || reservedCPUs != "" {
			return types.CPUManagerConfig{}, nil, fmt.Errorf("--cpu-manager-policy-options and --reserved-cpus require --cpu-manager-policy=%s", types.CPUManagerPolicyStatic)
		}
		return types.CPUManagerConfig{Policy: types.CPUManagerPolicyNone}, reserved, nil
	case types.CPUManagerPolicyStatic:
	default:
		return types.CPUManagerConfig{}, nil, fmt.Errorf("invalid --cpu-manager-policy %q: must be %s or %s", policy, types.CPUManagerPolicyNone, types.CPUManagerPolicyStatic)
	}

	parsedOptions, err := parseOptions(options)
	if err != nil {
		return types.CPUManagerConfig{}, nil, err
	}

	// Upstream defines an explicit cpuset as taking precedence over the cpu entry in
	// --system-reserved, and the kubelet overwrites it on that basis. Honour that
	// rather than rejecting the pair, but say so here: the kubelet logs the override
	// only in its own output, which is easy to miss.
	if reservedCPUs != "" && hasReservedCPU {
		log.Warn().Str("component", "cpumanager").Msgf("--reserved-cpus %q takes precedence over --system-reserved cpu=%s, which is ignored", reservedCPUs, reservedCPUQuantity)
	}

	if reservedCPUs == "" && !hasReservedCPU {
		reservedCPUs = defaultReservedCPUs
		log.Warn().Str("component", "cpumanager").Msgf("neither --reserved-cpus nor --system-reserved cpu is set, defaulting to cpu %q", reservedCPUs)
	}

	// Reserving by count leaves core selection to the kubelet, which picks by
	// topology. Say so, because host-level isolation cannot be aligned to cores
	// kubesolo did not choose.
	if reservedCPUs == "" {
		log.Info().Str("component", "cpumanager").Msgf("cpu pinning enabled: %s cpu(s) reserved for the host via --system-reserved. the kubelet chooses which cores, so isolcpus and IRQ affinity cannot be aligned to them — use --reserved-cpus to name them", reservedCPUQuantity)
		return types.CPUManagerConfig{
			Policy:        types.CPUManagerPolicyStatic,
			PolicyOptions: parsedOptions,
		}, reserved, nil
	}

	cpus, err := cpuset.Parse(reservedCPUs)
	if err != nil {
		return types.CPUManagerConfig{}, nil, fmt.Errorf("invalid --reserved-cpus %q: %v", reservedCPUs, err)
	}

	for _, cpu := range cpus.List() {
		if cpu >= numCPU {
			return types.CPUManagerConfig{}, nil, fmt.Errorf("invalid --reserved-cpus %q: CPU %d does not exist, this host has %d CPUs", reservedCPUs, cpu, numCPU)
		}
	}

	if numCPU-cpus.Size() < 1 {
		return types.CPUManagerConfig{}, nil, fmt.Errorf("--reserved-cpus %q reserves all %d CPUs, leaving none to pin workloads to", reservedCPUs, numCPU)
	}

	// Logged as counts as well as the cpuset, because "0" reserves one CPU rather
	// than none and the distinction is easy to misread.
	log.Info().Str("component", "cpumanager").Msgf("cpu pinning enabled: %d of %d cpus reserved for the host (cpuset %q), %d available for exclusive allocation",
		cpus.Size(), numCPU, reservedCPUs, numCPU-cpus.Size())

	return types.CPUManagerConfig{
		Policy:        types.CPUManagerPolicyStatic,
		PolicyOptions: parsedOptions,
		ReservedCPUs:  reservedCPUs,
	}, reserved, nil
}

// parseSystemReserved turns a comma-separated key=value list into the resource map the
// kubelet config expects, rejecting resources the kubelet does not support and
// quantities it cannot parse.
func parseSystemReserved(systemReserved string) (map[string]string, error) {
	if systemReserved == "" {
		return nil, nil
	}

	parsed := make(map[string]string)
	for _, entry := range strings.Split(systemReserved, ",") {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			return nil, fmt.Errorf("invalid --system-reserved entry %q: expected key=value", entry)
		}

		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !slices.Contains(systemReservedResources, name) {
			return nil, fmt.Errorf("unsupported --system-reserved resource %q: supported resources are %s", name, strings.Join(systemReservedResources, ", "))
		}

		if _, err := resource.ParseQuantity(value); err != nil {
			return nil, fmt.Errorf("invalid --system-reserved quantity %q for %q: %v", value, name, err)
		}

		parsed[name] = value
	}

	return parsed, nil
}

// checkReservedCPUCount rejects a --system-reserved cpu entry that would leave the
// node with nothing to run workloads on. The kubelet takes the ceiling of the value,
// since a fractional CPU cannot be exclusively allocated.
func checkReservedCPUCount(quantity string, numCPU int) error {
	parsed, err := resource.ParseQuantity(quantity)
	if err != nil {
		return fmt.Errorf("invalid --system-reserved quantity %q for %q: %v", quantity, resourceCPU, err)
	}

	count := int(math.Ceil(float64(parsed.MilliValue()) / 1000))
	if count >= numCPU {
		return fmt.Errorf("--system-reserved cpu=%s reserves %d of this host's %d CPUs, leaving none for workloads", quantity, count, numCPU)
	}

	return nil
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
