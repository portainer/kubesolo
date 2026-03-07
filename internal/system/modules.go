package system

import (
	"os/exec"

	"github.com/rs/zerolog/log"
)

// requiredModules lists kernel modules needed by kubelet and kube-proxy.
// Modules that are built-in to the kernel will fail modprobe gracefully.
var requiredModules = []string{
	"br_netfilter",
	"overlay",
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

// LoadRequiredModules attempts to load kernel modules required by Kubernetes
// networking components. Failures are logged as warnings and do not prevent
// startup — the module may be built-in or simply unavailable.
func LoadRequiredModules() {
	for _, mod := range requiredModules {
		out, err := exec.Command("modprobe", mod).CombinedOutput()
		if err != nil {
			log.Warn().Str("component", "kubesolo").Msgf("modprobe %s: %v (output: %s) — module may be built-in or unavailable", mod, err, string(out))
			continue
		}
		log.Debug().Str("component", "kubesolo").Msgf("loaded kernel module: %s", mod)
	}
}
