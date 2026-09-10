package config

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/portainer/kubesolo/types"
)

// Mutability says whether a setting can be changed on an existing install.
type Mutability int

const (
	// MutabilityRestart means the change takes effect when KubeSolo restarts.
	// Almost everything is this: settings are read once during bootstrap and
	// baked into types.Embedded, which each service is constructed from.
	MutabilityRestart Mutability = iota

	// MutabilityImmutable means the change cannot be applied at all. Only Path
	// is immutable: every certificate, the kine database and all containerd
	// state live below it, and nothing migrates them.
	MutabilityImmutable
)

func (m Mutability) String() string {
	if m == MutabilityImmutable {
		return "immutable"
	}
	return "restart"
}

// Field describes one configurable setting once, so that the config path, the
// frozen flag, the environment variable and the mutability class cannot drift
// apart. Everything that needs to enumerate settings — the loader, the dotted
// path CLI, the API schema and the restart-required report — reads this.
type Field struct {
	// ConfigPath is the dotted path in the config document, e.g. "network.nodeIP".
	ConfigPath string

	// Flag is the frozen CLI flag, without leading dashes. Empty for settings
	// that never had one.
	Flag string

	// Envar is the environment variable, empty if there is none.
	Envar string

	// Secret marks a value that must be redacted in API responses.
	Secret bool

	Mutability Mutability

	// Get reads the current value, for display and for diffing.
	Get func(*types.Config) any

	// Set applies a value in its flag/environment string form. The config file
	// is not routed through here — it is unmarshalled directly.
	Set func(*types.Config, string) error
}

// Registry is every configurable setting. It is the source of truth: a setting
// absent from here is invisible to the loader, the CLI and the API.
//
// The table is built once and shared. Treat the result as read-only — it is a
// constant, and callers on the request path would otherwise rebuild twenty-nine
// closures apiece.
//
// --full is deliberately absent. It has had no effect for several releases; the
// flag remains and still warns, but there is nothing to configure.
var Registry = sync.OnceValue(buildRegistry)

func buildRegistry() []Field {
	return []Field{
		{
			ConfigPath: "path",
			Flag:       "path",
			Envar:      "KUBESOLO_PATH",
			Mutability: MutabilityImmutable,
			Get:        func(c *types.Config) any { return c.Path },
			Set:        func(c *types.Config, v string) error { c.Path = v; return nil },
		},
		{
			ConfigPath: "logging.debug",
			Flag:       "debug",
			Envar:      "KUBESOLO_DEBUG",
			Get:        func(c *types.Config) any { return c.Logging.Debug },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Logging.Debug, v) },
		},
		{
			ConfigPath: "logging.pprof",
			Flag:       "pprof-server",
			Envar:      "KUBESOLO_PPROF_SERVER",
			Get:        func(c *types.Config) any { return c.Logging.Pprof },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Logging.Pprof, v) },
		},
		{
			ConfigPath: "network.nodeIP",
			Flag:       "node-ip",
			Envar:      "KUBESOLO_NODE_IP",
			Get:        func(c *types.Config) any { return c.Network.NodeIP },
			Set:        func(c *types.Config, v string) error { c.Network.NodeIP = v; return nil },
		},
		{
			ConfigPath: "network.mtu",
			Flag:       "mtu",
			Envar:      "KUBESOLO_MTU",
			Get:        func(c *types.Config) any { return c.Network.MTU },
			Set:        func(c *types.Config, v string) error { return setInt(&c.Network.MTU, v) },
		},
		{
			ConfigPath: "network.disableIPv6",
			Flag:       "disable-ipv6",
			Envar:      "KUBESOLO_DISABLE_IPV6",
			Get:        func(c *types.Config) any { return c.Network.DisableIPv6 },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Network.DisableIPv6, v) },
		},
		{
			ConfigPath: "network.loadBalancer.enabled",
			Flag:       "load-balancer",
			Envar:      "KUBESOLO_LOAD_BALANCER",
			Get:        func(c *types.Config) any { return c.Network.LoadBalancer.Enabled },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Network.LoadBalancer.Enabled, v) },
		},
		{
			ConfigPath: "network.loadBalancer.ip",
			Flag:       "load-balancer-ip",
			Envar:      "KUBESOLO_LOAD_BALANCER_IP",
			Get:        func(c *types.Config) any { return c.Network.LoadBalancer.IP },
			Set:        func(c *types.Config, v string) error { c.Network.LoadBalancer.IP = v; return nil },
		},
		{
			ConfigPath: "runtime.endpoint",
			Flag:       "container-runtime-endpoint",
			Envar:      "KUBESOLO_CONTAINER_RUNTIME_ENDPOINT",
			Get:        func(c *types.Config) any { return c.Runtime.Endpoint },
			Set:        func(c *types.Config, v string) error { c.Runtime.Endpoint = v; return nil },
		},
		{
			ConfigPath: "runtime.containerMode",
			Flag:       "container-mode",
			Envar:      "KUBESOLO_CONTAINER_MODE",
			Get:        func(c *types.Config) any { return c.Runtime.ContainerMode },
			Set: func(c *types.Config, v string) error {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return fmt.Errorf("expected a boolean, got %q", v)
				}
				c.Runtime.ContainerMode = &b
				return nil
			},
		},
		{
			ConfigPath: "pki.caCert",
			Envar:      "KUBESOLO_PKI_CA_CERT",
			Get:        func(c *types.Config) any { return c.PKI.CACert },
			Set:        func(c *types.Config, v string) error { c.PKI.CACert = v; return nil },
		},
		{
			ConfigPath: "pki.caKey",
			Envar:      "KUBESOLO_PKI_CA_KEY",
			Get:        func(c *types.Config) any { return c.PKI.CAKey },
			Set:        func(c *types.Config, v string) error { c.PKI.CAKey = v; return nil },
		},
		{
			ConfigPath: "kubernetes.kubelet.external",
			Envar:      "KUBESOLO_KUBELET_EXTERNAL",
			Get:        func(c *types.Config) any { return c.Kubernetes.Kubelet.External },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Kubernetes.Kubelet.External, v) },
		},
		{
			ConfigPath: "kubernetes.bootstrapToken",
			Envar:      "KUBESOLO_BOOTSTRAP_TOKEN",
			Secret:     true,
			Get:        func(c *types.Config) any { return c.Kubernetes.BootstrapToken },
			Set:        func(c *types.Config, v string) error { c.Kubernetes.BootstrapToken = v; return nil },
		},
		{
			ConfigPath: "kubernetes.nodeName",
			Envar:      "KUBESOLO_NODE_NAME",
			Get:        func(c *types.Config) any { return c.Kubernetes.NodeName },
			Set:        func(c *types.Config, v string) error { c.Kubernetes.NodeName = v; return nil },
		},
		{
			ConfigPath: "kubernetes.apiServer.extraSANs",
			Flag:       "apiserver-extra-sans",
			Envar:      "KUBESOLO_APISERVER_EXTRA_SANS",
			Get:        func(c *types.Config) any { return c.Kubernetes.APIServer.ExtraSANs },
			Set: func(c *types.Config, v string) error {
				c.Kubernetes.APIServer.ExtraSANs = splitList(v)
				return nil
			},
		},
		{
			ConfigPath: "kubernetes.apiServer.startupTimeoutSeconds",
			Flag:       "startup-timeout",
			Envar:      "KUBESOLO_STARTUP_TIMEOUT",
			Get:        func(c *types.Config) any { return c.Kubernetes.APIServer.StartupTimeoutSeconds },
			Set: func(c *types.Config, v string) error {
				return setInt(&c.Kubernetes.APIServer.StartupTimeoutSeconds, v)
			},
		},
		{
			ConfigPath: "kubernetes.kubelet.cpuManager.policy",
			Flag:       "cpu-manager-policy",
			Envar:      "KUBESOLO_CPU_MANAGER_POLICY",
			Get:        func(c *types.Config) any { return c.Kubernetes.Kubelet.CPUManager.Policy },
			Set: func(c *types.Config, v string) error {
				c.Kubernetes.Kubelet.CPUManager.Policy = v
				return nil
			},
		},
		{
			ConfigPath: "kubernetes.kubelet.cpuManager.policyOptions",
			Flag:       "cpu-manager-policy-options",
			Envar:      "KUBESOLO_CPU_MANAGER_POLICY_OPTIONS",
			Get:        func(c *types.Config) any { return c.Kubernetes.Kubelet.CPUManager.PolicyOptions },
			Set: func(c *types.Config, v string) error {
				m, err := parseKeyValues(v)
				if err != nil {
					return err
				}
				c.Kubernetes.Kubelet.CPUManager.PolicyOptions = m
				return nil
			},
		},
		{
			ConfigPath: "kubernetes.kubelet.cpuManager.reservedCPUs",
			Flag:       "reserved-cpus",
			Envar:      "KUBESOLO_RESERVED_CPUS",
			Get:        func(c *types.Config) any { return c.Kubernetes.Kubelet.CPUManager.ReservedCPUs },
			Set: func(c *types.Config, v string) error {
				c.Kubernetes.Kubelet.CPUManager.ReservedCPUs = v
				return nil
			},
		},
		{
			ConfigPath: "kubernetes.kubelet.systemReserved",
			Flag:       "system-reserved",
			Envar:      "KUBESOLO_SYSTEM_RESERVED",
			Get:        func(c *types.Config) any { return c.Kubernetes.Kubelet.SystemReserved },
			Set: func(c *types.Config, v string) error {
				m, err := parseKeyValues(v)
				if err != nil {
					return err
				}
				c.Kubernetes.Kubelet.SystemReserved = m
				return nil
			},
		},
		{
			ConfigPath: "storage.localPath.enabled",
			Flag:       "local-storage",
			Envar:      "KUBESOLO_LOCAL_STORAGE",
			Get:        func(c *types.Config) any { return c.Storage.LocalPath.Enabled },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Storage.LocalPath.Enabled, v) },
		},
		{
			ConfigPath: "storage.localPath.sharedPath",
			Flag:       "local-storage-shared-path",
			Envar:      "KUBESOLO_LOCAL_STORAGE_SHARED_PATH",
			Get:        func(c *types.Config) any { return c.Storage.LocalPath.SharedPath },
			Set:        func(c *types.Config, v string) error { c.Storage.LocalPath.SharedPath = v; return nil },
		},
		{
			ConfigPath: "storage.dbWALRepair",
			Flag:       "db-wal-repair",
			Envar:      "KUBESOLO_DB_WAL_REPAIR",
			Get:        func(c *types.Config) any { return c.Storage.DBWALRepair },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Storage.DBWALRepair, v) },
		},
		{
			ConfigPath: "portainer.edgeID",
			Flag:       "portainer-edge-id",
			Envar:      "KUBESOLO_PORTAINER_EDGE_ID",
			Get:        func(c *types.Config) any { return c.Portainer.EdgeID },
			Set:        func(c *types.Config, v string) error { c.Portainer.EdgeID = v; return nil },
		},
		{
			ConfigPath: "portainer.edgeKey",
			Flag:       "portainer-edge-key",
			Envar:      "KUBESOLO_PORTAINER_EDGE_KEY",
			Secret:     true,
			Get:        func(c *types.Config) any { return c.Portainer.EdgeKey },
			Set:        func(c *types.Config, v string) error { c.Portainer.EdgeKey = v; return nil },
		},
		{
			ConfigPath: "portainer.async",
			Flag:       "portainer-edge-async",
			Envar:      "KUBESOLO_PORTAINER_EDGE_ASYNC",
			Get:        func(c *types.Config) any { return c.Portainer.Async },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Portainer.Async, v) },
		},
		{
			ConfigPath: "portainer.image",
			Flag:       "portainer-edge-image",
			Envar:      "KUBESOLO_PORTAINER_EDGE_IMAGE",
			Get:        func(c *types.Config) any { return c.Portainer.Image },
			Set:        func(c *types.Config, v string) error { c.Portainer.Image = v; return nil },
		},
		{
			ConfigPath: "d2k.enabled",
			Flag:       "d2k",
			Envar:      "KUBESOLO_D2K",
			Get:        func(c *types.Config) any { return c.D2K.Enabled },
			Set:        func(c *types.Config, v string) error { return setBool(&c.D2K.Enabled, v) },
		},
		{
			ConfigPath: "d2k.namespace",
			Flag:       "d2k-namespace",
			Envar:      "KUBESOLO_D2K_NAMESPACE",
			Get:        func(c *types.Config) any { return c.D2K.Namespace },
			Set:        func(c *types.Config, v string) error { c.D2K.Namespace = v; return nil },
		},
		{
			ConfigPath: "metrics.enabled",
			Flag:       "metrics-server",
			Envar:      "KUBESOLO_METRICS_SERVER",
			Get:        func(c *types.Config) any { return c.Metrics.Enabled },
			Set:        func(c *types.Config, v string) error { return setBool(&c.Metrics.Enabled, v) },
		},
		{
			ConfigPath: "metrics.bindAddress",
			Flag:       "metrics-bind-address",
			Envar:      "KUBESOLO_METRICS_BIND_ADDRESS",
			Get:        func(c *types.Config) any { return c.Metrics.BindAddress },
			Set:        func(c *types.Config, v string) error { c.Metrics.BindAddress = v; return nil },
		},
		{
			ConfigPath: "api.enabled",
			Envar:      "KUBESOLO_API_ENABLED",
			Get:        func(c *types.Config) any { return c.API.Enabled },
			Set:        func(c *types.Config, v string) error { return setBool(&c.API.Enabled, v) },
		},
		{
			ConfigPath: "api.socketPath",
			Envar:      "KUBESOLO_API_SOCKET_PATH",
			Get:        func(c *types.Config) any { return c.API.SocketPath },
			Set:        func(c *types.Config, v string) error { c.API.SocketPath = v; return nil },
		},
	}
}

// FieldByPath looks up a single setting by its dotted config path.
func FieldByPath(path string) (Field, bool) {
	for _, f := range Registry() {
		if f.ConfigPath == path {
			return f, true
		}
	}
	return Field{}, false
}

func setBool(target *bool, v string) error {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("expected a boolean, got %q", v)
	}
	*target = b
	return nil
}

func setInt(target *int, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("expected an integer, got %q", v)
	}
	*target = n
	return nil
}

// splitList parses a comma-separated flag value into a list.
//
// Empty yields nil rather than [""]. The flag path has always produced the
// latter (strings.Split("", ",")), which reaches the API server certificate as
// an empty SAN; the config path does not reproduce it.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseKeyValues parses a comma-separated key=value flag value into a map,
// as used by --system-reserved and --cpu-manager-policy-options.
func parseKeyValues(v string) (map[string]string, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(v, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("expected key=value pairs, got %q", pair)
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out, nil
}
