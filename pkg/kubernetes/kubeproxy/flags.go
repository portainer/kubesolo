package kubeproxy

import (
	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
)

func (s *service) configureKubeProxyFlags(command *cobra.Command) {
	flags := command.Flags()

	// networking settings
	_ = flags.Set("kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("cluster-cidr", types.DefaultPodCIDR)
	_ = flags.Set("metrics-bind-address", "")

	// performance settings
	_ = flags.Set("oom-score-adj", "-998")
	_ = flags.Set("profiling", "false")

	// iptables settings
	_ = flags.Set("iptables-masquerade-bit", "14")
	_ = flags.Set("masquerade-all", "true")
	_ = flags.Set("proxy-mode", "iptables")
	_ = flags.Set("min-sync-period", "10s")

	if s.containerMode {
		// In container mode, avoid writing to /proc/sys/net/netfilter/nf_conntrack_max
		// which may be read-only depending on the container runtime.
		_ = flags.Set("conntrack-max-per-core", "0")
		_ = flags.Set("conntrack-min", "0")
	} else {
		_ = flags.Set("conntrack-max-per-core", "1024")
		_ = flags.Set("conntrack-min", "1024")
	}
}
