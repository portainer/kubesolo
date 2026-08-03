package network

import (
	"fmt"
	"net"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// minValidMTU is the RFC 791 IPv4 minimum. maxValidMTU is the largest value
// the MTU field in Linux netlink/ioctl interfaces can represent.
const (
	minValidMTU = 68
	maxValidMTU = 65535
)

// ifaceCandidate is the subset of net.Interface data selectMTU needs, kept
// separate so the selection logic is unit-testable without real interfaces.
type ifaceCandidate struct {
	mtu   int
	addrs []net.Addr
}

// selectMTU picks the MTU of the interface selectNodeIP would pick the node
// IP from: the first candidate with a private IPv4 address, falling back to
// the first candidate with any non-loopback IPv4 address. Candidates with a
// non-positive MTU are skipped since that reflects a down/virtual interface,
// not a usable value.
func selectMTU(candidates []ifaceCandidate) (int, error) {
	var firstNonLoopback int
	haveFirstNonLoopback := false
	for _, c := range candidates {
		if c.mtu <= 0 {
			continue
		}
		for _, addr := range c.addrs {
			_, private, ok := classifyIPv4(addr)
			if !ok {
				continue
			}
			if private {
				return c.mtu, nil
			}
			if !haveFirstNonLoopback {
				firstNonLoopback = c.mtu
				haveFirstNonLoopback = true
			}
		}
	}
	if haveFirstNonLoopback {
		return firstNonLoopback, nil
	}
	return types.DefaultMTU, fmt.Errorf("could not find non-loopback IPv4 interface")
}

// GetNodeMTU returns the MTU of whichever interface GetNodeIP would select
// the node IP from, so MTU detection stays consistent with node-IP selection
// on multi-NIC hosts. Returns types.DefaultMTU on any failure.
func GetNodeMTU() (int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return types.DefaultMTU, fmt.Errorf("failed to list network interfaces: %v", err)
	}

	candidates := make([]ifaceCandidate, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		candidates = append(candidates, ifaceCandidate{mtu: iface.MTU, addrs: addrs})
	}

	mtu, err := selectMTU(candidates)
	if err != nil {
		return types.DefaultMTU, err
	}
	return mtu, nil
}

// ResolveMTU returns the MTU to apply to the embedded CNI bridge and pod veth
// interfaces, and whether it was explicitly pinned via a valid override.
// override <= 0 means "not specified": auto-detect via GetNodeMTU, not
// pinned. An override outside the valid IPv4 MTU range [68, 65535] is
// rejected with a warning and falls back to auto-detection, not pinned — a
// typo must not silently switch the caller into pinned behavior. A
// valid override is always used as-is and pinned, even if it differs from
// the auto-detected value (soft warning only, analogous to isLocalIP's VIP
// warning for --node-ip).
func ResolveMTU(override int) (mtu int, pinned bool, err error) {
	if override <= 0 {
		mtu, err = GetNodeMTU()
		return mtu, false, err
	}
	if override < minValidMTU || override > maxValidMTU {
		log.Warn().Str("component", "network").Int("mtu", override).
			Msg("--mtu is outside the valid range (68-65535); ignoring it and auto-detecting the MTU")
		mtu, err = GetNodeMTU()
		return mtu, false, err
	}
	if detected, derr := GetNodeMTU(); derr == nil && detected != override {
		log.Warn().Str("component", "network").Int("mtu", override).Int("detected-mtu", detected).
			Msg("--mtu differs from the auto-detected interface MTU; using the override anyway")
	}
	return override, true, nil
}
