package system

import (
	"os"
	"os/exec"

	"github.com/rs/zerolog/log"
)

// commonModules are needed regardless of proxy mode
var commonModules = []string{
	"br_netfilter",
	"overlay",
}

// iptablesModules are needed when kube-proxy uses iptables mode
var iptablesModules = []string{
	"xt_MASQUERADE",
	"xt_conntrack",
	"xt_comment",
	"xt_addrtype",
	"xt_multiport",
	"xt_nat",
	"xt_recent",
	"xt_set",
	"xt_statistic",
	"xt_nfacct",
}

// nftablesModules are needed when kube-proxy uses nftables mode
var nftablesModules = []string{
	"nft_numgen",
	"nft_redir",
	"nft_limit",
	"nft_tproxy",
}

// LoadRequiredModules attempts to load kernel modules required by Kubernetes
// networking components. Detects whether the host supports iptables or nftables
// and loads the appropriate module set. Failures are logged as warnings and do
// not prevent startup — the module may be built-in or simply unavailable.
func LoadRequiredModules() {
	modules := commonModules

	if _, err := os.Stat("/proc/net/ip_tables_names"); err != nil {
		log.Info().Str("component", "kubesolo").Msg("iptables kernel modules not available, loading nftables modules")
		modules = append(modules, nftablesModules...)
	} else {
		log.Info().Str("component", "kubesolo").Msg("iptables kernel modules available, loading iptables modules")
		modules = append(modules, iptablesModules...)
	}

	for _, mod := range modules {
		out, err := exec.Command("modprobe", mod).CombinedOutput()
		if err != nil {
			log.Warn().Str("component", "kubesolo").Msgf("modprobe %s: %v (output: %s) — module may be built-in or unavailable", mod, err, string(out))
			continue
		}
		log.Debug().Str("component", "kubesolo").Msgf("loaded kernel module: %s", mod)
	}
}
