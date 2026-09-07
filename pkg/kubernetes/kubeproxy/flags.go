package kubeproxy

import (
	"os"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// detectProxyMode returns "nftables" if the iptables kernel modules are
// unavailable (e.g., ip_tables or xt_REJECT not compiled), otherwise "iptables".
// kube-proxy nftables mode is stable since Kubernetes 1.31.
func detectProxyMode() string {
	if _, err := os.Stat("/proc/net/ip_tables_names"); err != nil {
		log.Info().Str("component", "kubeproxy").Msg("iptables kernel modules not available, using nftables proxy mode")
		return "nftables"
	}
	return "iptables"
}

func (s *service) configureKubeProxyFlags(command *cobra.Command) {
	flags := command.Flags()

	proxyMode := detectProxyMode()

	// networking settings
	_ = flags.Set("kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("cluster-cidr", types.DefaultPodCIDR)
	_ = flags.Set("metrics-bind-address", "")

	// performance settings
	_ = flags.Set("oom-score-adj", "-998")

	// proxy mode and conntrack settings
	_ = flags.Set("proxy-mode", proxyMode)
	if s.containerMode {
		// In container mode, avoid writing to /proc/sys/net/netfilter which may be
		// read-only depending on the container runtime. Zero leaves the kernel
		// value alone. The timeouts matter as much as the maximums: kube-proxy
		// exits outright when it cannot write them. The UDP timeouts already
		// default to zero, so only these four are set.
		_ = flags.Set("conntrack-max-per-core", "0")
		_ = flags.Set("conntrack-min", "0")
		_ = flags.Set("conntrack-tcp-timeout-established", "0s")
		_ = flags.Set("conntrack-tcp-timeout-close-wait", "0s")
	}

	if proxyMode == "iptables" {
		_ = flags.Set("masquerade-all", "true")
	}
}
