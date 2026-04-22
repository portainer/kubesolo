package network

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

const masqueradeComment = "kubesolo: pod masquerade"

// EnsurePodMasquerade programs a SNAT/masquerade rule for pod egress traffic so
// pods can reach external IPs. It is idempotent and mode-aware: iptables on
// systems that have the ip_tables kernel module, nftables otherwise.
//
// This must be called before kubelet starts (so no pod ever starts without SNAT
// in place) and after flushNftablesNat (to restore the rule after the nat table
// is flushed for kube-proxy compatibility).
func EnsurePodMasquerade(podCIDR string) error {
	if _, err := os.Stat("/proc/net/ip_tables_names"); err == nil {
		return ensureIPTablesMasquerade(podCIDR)
	}
	return ensureNftablesMasquerade(podCIDR)
}

func ensureIPTablesMasquerade(podCIDR string) error {
	args := []string{
		"-t", "nat", "-C", "POSTROUTING",
		"-s", podCIDR, "!", "-d", podCIDR,
		"-m", "comment", "--comment", masqueradeComment,
		"-j", "MASQUERADE",
	}

	if err := exec.Command("iptables", args...).Run(); err == nil {
		log.Debug().Str("component", "network").Msg("pod masquerade rule already present (iptables)")
		return nil
	}

	args[2] = "-A"
	if out, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables: failed to add pod masquerade rule: %v (output: %s)", err, out)
	}

	log.Info().Str("component", "network").
		Str("cidr", podCIDR).
		Msg("added pod masquerade rule (iptables)")
	return nil
}

func ensureNftablesMasquerade(podCIDR string) error {
	out, _ := exec.Command("nft", "list", "table", "ip", types.DefaultNftMasqTable).CombinedOutput()
	if strings.Contains(string(out), masqueradeComment) {
		log.Debug().Str("component", "network").Msg("pod masquerade rule already present (nftables)")
		return nil
	}

	cmds := [][]string{
		{"nft", "add", "table", "ip", types.DefaultNftMasqTable},
		{"nft", "add", "chain", "ip", types.DefaultNftMasqTable, "postrouting",
			"{ type nat hook postrouting priority srcnat; policy accept; }"},
		{"nft", "add", "rule", "ip", types.DefaultNftMasqTable, "postrouting",
			"ip", "saddr", podCIDR, "ip", "daddr", "!=", podCIDR,
			"masquerade", "comment", `"` + masqueradeComment + `"`},
	}

	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("nft %s: %v (output: %s)", strings.Join(args[1:], " "), err, out)
		}
	}

	log.Info().Str("component", "network").
		Str("cidr", podCIDR).
		Str("table", types.DefaultNftMasqTable).
		Msg("added pod masquerade rule (nftables)")
	return nil
}
