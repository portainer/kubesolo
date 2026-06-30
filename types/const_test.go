package types

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests assert relationships between the network defaults rather than
// re-stating the literals. They catch a class of real misconfiguration: a
// service IP that falls outside the service CIDR, an unparseable CIDR, or the
// DNS and kubernetes service IPs colliding.

func TestNetworkDefaultsParse(t *testing.T) {
	for _, cidr := range []string{DefaultPodCIDR, DefaultServiceClusterIPRange} {
		_, _, err := net.ParseCIDR(cidr)
		assert.NoErrorf(t, err, "CIDR %q must parse", cidr)
	}
	for _, ip := range []string{DefaultCoreDNSIP, DefaultKubernetesServiceIP} {
		assert.NotNilf(t, net.ParseIP(ip), "IP %q must parse", ip)
	}
}

func TestServiceIPsWithinServiceCIDR(t *testing.T) {
	_, svcNet, err := net.ParseCIDR(DefaultServiceClusterIPRange)
	require.NoError(t, err)

	assert.Truef(t, svcNet.Contains(net.ParseIP(DefaultCoreDNSIP)),
		"CoreDNS IP %s must lie within service CIDR %s", DefaultCoreDNSIP, DefaultServiceClusterIPRange)
	assert.Truef(t, svcNet.Contains(net.ParseIP(DefaultKubernetesServiceIP)),
		"kubernetes service IP %s must lie within service CIDR %s", DefaultKubernetesServiceIP, DefaultServiceClusterIPRange)
}

func TestPodAndServiceCIDRsDoNotOverlap(t *testing.T) {
	_, podNet, err := net.ParseCIDR(DefaultPodCIDR)
	require.NoError(t, err)
	_, svcNet, err := net.ParseCIDR(DefaultServiceClusterIPRange)
	require.NoError(t, err)

	assert.False(t, podNet.Contains(svcNet.IP), "pod CIDR must not contain the service network")
	assert.False(t, svcNet.Contains(podNet.IP), "service CIDR must not contain the pod network")
}

func TestCoreDNSAndKubernetesServiceIPsDiffer(t *testing.T) {
	assert.NotEqual(t, DefaultCoreDNSIP, DefaultKubernetesServiceIP)
}
