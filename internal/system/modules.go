package system

import (
	"os/exec"
	"strings"

	"github.com/rs/zerolog/log"
)

// commonModules are needed regardless of networking backend
var commonModules = []string{
	"br_netfilter",
	"overlay",
}

// cniXtablesModules are xtables match/target modules required by the CNI bridge
// and portmap plugins. These are needed on both legacy iptables and iptables-nft
// systems (nft_compat translates them on nftables kernels).
var cniXtablesModules = []string{
	"xt_comment",
	"xt_conntrack",
	"xt_MASQUERADE",
	"xt_addrtype",
	"xt_multiport",
	"xt_nat",
}

// nftablesModules are loaded on systems using nf_tables (e.g., nova8OS).
// Includes nft_compat for iptables-nft translation of xt_* extensions.
var nftablesModules = []string{
	"nft_compat",
	"nft_numgen",
	"nft_redir",
	"nft_limit",
	"nft_tproxy",
}

// iptablesModules are loaded on legacy iptables systems (e.g., Ubuntu)
var iptablesModules = []string{
	"ip_tables",
	"iptable_filter",
	"iptable_nat",
	"nf_conntrack",
}

// LoadRequiredModules attempts to load kernel modules required by Kubernetes
// networking components. Detects whether the host uses nf_tables or legacy
// iptables and loads the appropriate backend modules, plus xtables match/target
// modules needed by the CNI bridge plugin in both cases.
// Failures are logged as warnings and do not prevent startup — the module may
// be built-in or simply unavailable.
func LoadRequiredModules() {
	modules := append(commonModules, cniXtablesModules...)

	if isNftBackend() {
		log.Info().Str("component", "kubesolo").Msg("nf_tables backend detected, loading nftables modules")
		modules = append(modules, nftablesModules...)
	} else {
		log.Info().Str("component", "kubesolo").Msg("legacy iptables backend detected, loading iptables modules")
		modules = append(modules, iptablesModules...)
	}

	log.Debug().Str("component", "kubesolo").Msgf("loading %d kernel modules", len(modules))
	for _, mod := range modules {
		out, err := exec.Command("modprobe", mod).CombinedOutput()
		if err != nil {
			log.Warn().Str("component", "kubesolo").Msgf("modprobe %s: %v (output: %s) — module may be built-in or unavailable", mod, err, string(out))
			continue
		}
		log.Debug().Str("component", "kubesolo").Msgf("loaded kernel module: %s", mod)
	}
}

// isNftBackend returns true if the iptables binary uses the nf_tables backend.
// Parses "iptables --version" output which contains "(nf_tables)" or "(legacy)".
func isNftBackend() bool {
	out, err := exec.Command("iptables", "--version").CombinedOutput()
	if err != nil {
		log.Warn().Str("component", "kubesolo").Msgf("failed to detect iptables backend: %v — assuming nf_tables", err)
		return true
	}
	version := string(out)
	log.Debug().Str("component", "kubesolo").Msgf("iptables version: %s", strings.TrimSpace(version))
	return strings.Contains(version, "(nf_tables)")
}
