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
		if got := flag.Value.String(); got != "0" && got != "0s" {
			t.Errorf("%s = %q, want zero so kube-proxy leaves the kernel value alone", name, got)
		}
	}
}

// Outside container mode kube-proxy owns the host and should tune conntrack as
// it normally would, so the defaults must be left in place.
func TestHostModeKeepsConntrackDefaults(t *testing.T) {
	command := app.NewProxyCommand()
	(&service{containerMode: false}).configureKubeProxyFlags(command)

	if got := command.Flags().Lookup("conntrack-tcp-timeout-established").Value.String(); got == "0s" {
		t.Error("conntrack-tcp-timeout-established was zeroed outside container mode")
	}
}
