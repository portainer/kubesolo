package network

import (
	"os"
	"os/exec"
	"sync"

	"github.com/rs/zerolog/log"
)

// missingIPTablesWarning logs the fallback once, since the masquerade rule is
// re-checked periodically for the life of the process.
var missingIPTablesWarning sync.Once

// UseIPTables reports whether kubesolo programs netfilter through iptables
// rather than nftables. Both the kernel tables and the iptables binary have to
// be present: kubesolo modprobes ip_tables on every start, so the kernel side
// alone says nothing about whether the binary is installed, and RHEL 10 ships
// the module without iptables. Picking iptables mode there crash-loops kubesolo
// on its first iptables call, while nftables mode works.
func UseIPTables() bool {
	if _, err := os.Stat("/proc/net/ip_tables_names"); err != nil {
		return false
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		missingIPTablesWarning.Do(func() {
			log.Warn().Str("component", "network").
				Msg("ip_tables kernel support is present but the iptables binary is not on the PATH, using nftables")
		})
		return false
	}
	return true
}
