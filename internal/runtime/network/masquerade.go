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
	present, err := nftRulePresent(types.DefaultNftMasqTable, masqueradeComment)
	if err != nil {
		return err
	}
	if present {
		log.Debug().Str("component", "network").Msg("pod masquerade rule already present (nftables)")
		return nil
	}

	// Create table — idempotent: nft add table succeeds even if it already exists.
	if out, err := exec.Command("nft", "add", "table", "ip", types.DefaultNftMasqTable).CombinedOutput(); err != nil {
		return fmt.Errorf("nft add table %s: %v (output: %s)", types.DefaultNftMasqTable, err, out)
	}

	// Create base chain — tolerate "already exists" because the chain may be
	// present from a previous partial run while the masquerade rule is missing.
	chainOut, chainErr := exec.Command("nft", "add", "chain", "ip", types.DefaultNftMasqTable, "postrouting",
		"{ type nat hook postrouting priority srcnat; policy accept; }").CombinedOutput()
	if chainErr != nil && !strings.Contains(strings.ToLower(string(chainOut)), "already exists") {
		return fmt.Errorf("nft add chain postrouting: %v (output: %s)", chainErr, chainOut)
	}

	// Add masquerade rule.
	ruleArgs := []string{
		"add", "rule", "ip", types.DefaultNftMasqTable, "postrouting",
		"ip", "saddr", podCIDR, "ip", "daddr", "!=", podCIDR,
		"masquerade", "comment", `"` + masqueradeComment + `"`,
	}
	if out, err := exec.Command("nft", ruleArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("nft add rule masquerade: %v (output: %s)", err, out)
	}

	log.Info().Str("component", "network").
		Str("cidr", podCIDR).
		Str("table", types.DefaultNftMasqTable).
		Msg("added pod masquerade rule (nftables)")
	return nil
}

// nftRulePresent reports whether the named table contains a rule matching
// comment. It returns an error only for unexpected failures — a missing table
// is not an error, it simply means the rule is absent.
func nftRulePresent(table, comment string) (bool, error) {
	out, err := exec.Command("nft", "list", "table", "ip", table).CombinedOutput()
	if err != nil {
		outLower := strings.ToLower(string(out))
		if strings.Contains(outLower, "no such file") ||
			strings.Contains(outLower, "table not found") ||
			strings.Contains(outLower, "no such table") {
			return false, nil
		}
		return false, fmt.Errorf("nft list table %s: %v (output: %s)", table, err, out)
	}
	return strings.Contains(string(out), comment), nil
}
