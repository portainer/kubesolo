package config

import (
	"reflect"
	"runtime"
	"strings"

	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/types"
)

// ContainerConfigFile is where a KubeSolo container reads its configuration:
// a volume of its own, so that a reset, which wipes the data volume, keeps the
// settings, as a reset of a host install keeps /etc/kubesolo.
const ContainerConfigFile = types.DefaultConfigFile

// ContainerIgnored lists the KubeSolo settings that mean nothing inside a
// container, because kubesoloctl or the container decides them. A container
// instance leaves them out of its configuration, and kubesoloctl reports them as
// ignored:
//
//   - path: the data volume is mounted at the default path.
//   - network.nodeIP: the node is the container's address on its network.
//   - network.mtu: the container's network decides it, and KubeSolo detects it
//     from its own interface.
//   - network.loadBalancer.ip: LoadBalancer addresses are the container's own;
//     the host reaches services through published ports.
//   - runtime.*: a container always runs the embedded containerd, in container
//     mode.
//   - kubernetes.nodeName: the node name is the container's hostname, which
//     kubesoloctl keeps stable when it replaces the container.
//   - the CPU manager: exclusive cores are bounded by the container's cpuset,
//     which KubeSolo does not control.
//   - api.*: the API serves upgrades, which a container cannot do to itself, and
//     its socket is not reachable from the host.
//   - kubernetes.apiServer.port, d2k.port: kubesoloctl publishes the container's
//     6443 and 2376 on host ports of its own choosing, so it leaves them out of
//     a container's configuration.
var ContainerIgnored = []string{
	"path",
	"network.nodeIP",
	"network.mtu",
	"network.loadBalancer.ip",
	"runtime.endpoint",
	"runtime.containerMode",
	"kubernetes.nodeName",
	"kubernetes.kubelet.cpuManager.policy",
	"kubernetes.kubelet.cpuManager.policyOptions",
	"kubernetes.kubelet.cpuManager.reservedCPUs",
	"api.enabled",
	"api.socketPath",
	"kubernetes.apiServer.port",
	"d2k.port",
}

// Flags returns the installer's settings as KubeSolo flags.
func (c *Config) Flags() []string { return c.kubesoloFlags() }

// ContainerConfig resolves KubeSolo flags — the installer's, or an existing
// container's command line — into the configuration of a container instance.
// The settings in ContainerIgnored are left out before anything is parsed, so
// that a value that means nothing in a container cannot fail the install; the
// rest is validated as KubeSolo will. It returns the document, the flags that
// remain (for a release that predates the configuration file), and the flags it
// left out, a --path naming the default path aside.
func ContainerConfig(args []string) (doc *types.Config, kept, dropped []string, warnings []kubesoloconfig.Warning, err error) {
	ignored := map[string]bool{}
	for _, p := range ContainerIgnored {
		if f, ok := kubesoloconfig.FieldByPath(p); ok && f.Flag != "" {
			ignored[f.Flag] = true
		}
	}
	for _, a := range args {
		name, _, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if !ignored[name] {
			kept = append(kept, a)
			continue
		}
		if a != "--path="+DefaultPath {
			dropped = append(dropped, "--"+name)
		}
	}

	values, setByUser := splitFlagArgs(kept)
	doc, warnings, err = kubesoloconfig.LoadWithoutEnv("", kubesoloconfig.FlagValues{Values: values, SetByUser: setByUser})
	if err != nil {
		return nil, kept, dropped, warnings, err
	}
	validationWarnings, err := kubesoloconfig.Validate(doc, kubesoloconfig.Host{
		NumCPU:        runtime.NumCPU(),
		GOARCH:        runtime.GOARCH,
		ContainerMode: true,
	})
	return doc, kept, dropped, append(warnings, validationWarnings...), err
}

// ContainerIgnoredSet returns the settings in ContainerIgnored that doc changes
// from what a container instance has: a configuration a user edited to set one.
func ContainerIgnoredSet(doc *types.Config) []string {
	base, _, _, _, err := ContainerConfig(nil)
	if err != nil {
		return nil
	}
	var set []string
	for _, p := range ContainerIgnored {
		f, ok := kubesoloconfig.FieldByPath(p)
		if ok && !reflect.DeepEqual(f.Get(doc), f.Get(base)) {
			set = append(set, p)
		}
	}
	return set
}

// UsesConfigFile reports whether a KubeSolo command line reads a configuration
// file rather than flags.
func UsesConfigFile(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "--config=") {
			return true
		}
	}
	return false
}
