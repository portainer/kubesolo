package coredns

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCoreDNSConfig_Forwarder(t *testing.T) {
	// Container mode has no usable /etc/resolv.conf, so it forwards to public
	// resolvers; host mode forwards to the host resolv.conf.
	container := coreDNSConfig(true, false)
	assert.Contains(t, container, "forward . 1.1.1.1 8.8.8.8")
	assert.NotContains(t, container, "/etc/resolv.conf")

	host := coreDNSConfig(false, false)
	assert.Contains(t, host, "forward . /etc/resolv.conf")
	assert.NotContains(t, host, "1.1.1.1 8.8.8.8")
}

func TestCoreDNSConfig_IPv6Toggle(t *testing.T) {
	withV6 := coreDNSConfig(false, false)
	assert.Contains(t, withV6, "ip6.arpa", "IPv6 enabled must include ip6.arpa zones")

	withoutV6 := coreDNSConfig(false, true)
	assert.NotContains(t, withoutV6, "ip6.arpa", "disableIPv6 must drop ip6.arpa zones")
}

func TestCoreDNSConfig_Invariants(t *testing.T) {
	// Regardless of mode, the Corefile must keep the kubernetes plugin and the
	// health/readiness endpoints the deployment probes rely on.
	for _, cm := range []bool{true, false} {
		for _, v6 := range []bool{true, false} {
			cfg := coreDNSConfig(cm, v6)
			assert.Truef(t, strings.Contains(cfg, "kubernetes cluster.local"),
				"missing kubernetes plugin (containerMode=%v disableIPv6=%v)", cm, v6)
			assert.Containsf(t, cfg, "health :8080", "missing health endpoint (containerMode=%v disableIPv6=%v)", cm, v6)
			assert.Containsf(t, cfg, "ready :8181", "missing ready endpoint (containerMode=%v disableIPv6=%v)", cm, v6)
		}
	}
}
