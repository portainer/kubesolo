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
	if !s.fullMode {
		_ = flags.Set("profiling", "false")
		_ = flags.Set("conntrack-max-per-core", "1024")
		_ = flags.Set("conntrack-min", "1024")
		_ = flags.Set("min-sync-period", "10s")
	}

	if proxyMode == "iptables" {
		_ = flags.Set("masquerade-all", "true")
	}
}
