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
	// -w 5: wait up to 5 s for the xtables lock. This function is called during
	// kube-proxy startup when other processes may also be modifying iptables;
	// without -w, concurrent access causes "Resource temporarily unavailable".
	args := []string{
		"-w", "5",
		"-t", "nat", "-C", "POSTROUTING",
		"-s", podCIDR, "!", "-d", podCIDR,
		"-m", "comment", "--comment", masqueradeComment,
		"-j", "MASQUERADE",
	}

	if err := exec.Command("iptables", args...).Run(); err == nil {
		log.Debug().Str("component", "network").Msg("pod masquerade rule already present (iptables)")
		return nil
	}

	args[4] = "-A"
	if out, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables: failed to add pod masquerade rule: %v (output: %s)", err, out)
	}

	log.Info().Str("component", "network").
		Str("cidr", podCIDR).
		Msg("added pod masquerade rule (iptables)")
	return nil
}

func nftCombinedOutput(args ...string) ([]byte, error) {
	return exec.Command("nft", args...).CombinedOutput()
}

func nftAlreadyExists(out []byte) bool {
	msg := strings.ToLower(string(out))
	return strings.Contains(msg, "file exists") || strings.Contains(msg, "already exists")
}

// ensureNftObject ensures an nftables object (table or chain) exists. It tries
// listArgs first; if the object is absent it runs addArgs, tolerating a
// concurrent creation racing us to it.
func ensureNftObject(listArgs, addArgs []string) error {
	if _, err := nftCombinedOutput(listArgs...); err == nil {
		return nil
	}
	if out, err := nftCombinedOutput(addArgs...); err != nil && !nftAlreadyExists(out) {
		return fmt.Errorf("nft %s: %v (output: %s)", strings.Join(addArgs, " "), err, out)
	}
	return nil
}

func ensureNftablesMasquerade(podCIDR string) error {
	if err := ensureNftObject(
		[]string{"list", "table", "ip", types.DefaultNftMasqTable},
		[]string{"add", "table", "ip", types.DefaultNftMasqTable},
	); err != nil {
		return err
	}

	if err := ensureNftObject(
		[]string{"list", "chain", "ip", types.DefaultNftMasqTable, "postrouting"},
		[]string{"add", "chain", "ip", types.DefaultNftMasqTable, "postrouting",
			"{ type nat hook postrouting priority srcnat; policy accept; }"},
	); err != nil {
		return err
	}

	out, err := nftCombinedOutput("list", "chain", "ip", types.DefaultNftMasqTable, "postrouting")
	if err != nil {
		return fmt.Errorf("nft list chain ip %s postrouting: %v (output: %s)", types.DefaultNftMasqTable, err, out)
	}
	if strings.Contains(string(out), masqueradeComment) {
		log.Debug().Str("component", "network").Msg("pod masquerade rule already present (nftables)")
		return nil
	}

	args := []string{
		"add", "rule", "ip", types.DefaultNftMasqTable, "postrouting",
		"ip", "saddr", podCIDR, "ip", "daddr", "!=", podCIDR,
		"masquerade", "comment", `"` + masqueradeComment + `"`,
	}
	if out, err := nftCombinedOutput(args...); err != nil {
		return fmt.Errorf("nft %s: %v (output: %s)", strings.Join(args, " "), err, out)
	}

	log.Info().Str("component", "network").
		Str("cidr", podCIDR).
		Str("table", types.DefaultNftMasqTable).
		Msg("added pod masquerade rule (nftables)")
	return nil
}
