package kubeproxy

import (
	"testing"

	"k8s.io/kubernetes/cmd/kube-proxy/app"
)

// TestContainerModeLeavesConntrackAlone pins the whole conntrack set to zero in
// container mode.
//
// kube-proxy writes each of these to /proc/sys/net/netfilter, which runc mounts
// read-only unless the runtime clears it. Zero is the only value that stops the
// write. Missing any single one of them is fatal — kube-proxy exits with
// "read-only file system" — and the maximums alone are not enough, which is the
// bug this pins.
func TestContainerModeLeavesConntrackAlone(t *testing.T) {
	zeroed := []string{
		"conntrack-max-per-core",
		"conntrack-min",
		"conntrack-tcp-timeout-established",
		"conntrack-tcp-timeout-close-wait",
		"conntrack-udp-timeout",
		"conntrack-udp-timeout-stream",
	}

	command := app.NewProxyCommand()
	(&service{containerMode: true}).configureKubeProxyFlags(command)

	for _, name := range zeroed {
		flag := command.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("%s: flag not found on the kube-proxy command", name)
			continue
		}
		if got := flag.Value.String(); !isZero(got) {
			t.Errorf("%s = %q, want zero so kube-proxy leaves the kernel value alone", name, got)
		}
	}
}

// isZero covers both spellings: the counts render as "0" and the durations as
// "0s".
func isZero(value string) bool {
	return value == "0" || value == "0s"
}

// Outside container mode kube-proxy owns the host and should tune conntrack as
// it normally would, so the defaults must be left in place.
func TestHostModeKeepsConntrackDefaults(t *testing.T) {
	command := app.NewProxyCommand()
	(&service{containerMode: false}).configureKubeProxyFlags(command)

	const name = "conntrack-tcp-timeout-established"

	flag := command.Flags().Lookup(name)
	if flag == nil {
		t.Fatalf("%s: flag not found on the kube-proxy command", name)
	}

	if got := flag.Value.String(); isZero(got) {
		t.Errorf("%s = %q, want the kube-proxy default outside container mode", name, got)
	}
}
