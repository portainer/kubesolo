package pki

import (
	"net"
	"testing"

	"github.com/portainer/kubesolo/types"
	"github.com/stretchr/testify/assert"
)

func hasIP(ips []net.IP, want string) bool {
	target := net.ParseIP(want)
	for _, ip := range ips {
		if ip.Equal(target) {
			return true
		}
	}
	return false
}

func TestDefaultCertOptions_APIServerSANs(t *testing.T) {
	t.Run("pinned node IP scopes SANs to that IP plus service IP and localhost", func(t *testing.T) {
		e := types.Embedded{
			NodeIP:             "10.130.0.5",
			NodeIPSpecified:    true,
			APIServerExtraSANs: []string{"1.2.3.4", "kubesolo.local"},
		}
		opts := defaultCertOptions(APIServerCert, e)

		assert.True(t, hasIP(opts.IPAddresses, types.DefaultKubernetesServiceIP), "service IP must be present")
		assert.True(t, hasIP(opts.IPAddresses, "127.0.0.1"), "localhost must be present")
		assert.True(t, hasIP(opts.IPAddresses, "10.130.0.5"), "pinned node IP must be present")
		assert.True(t, hasIP(opts.IPAddresses, "1.2.3.4"), "extra SAN IP must be present")
		// service IP + 127.0.0.1 + node IP + one extra IP == 4, and no local
		// interface addresses are appended when the node IP is pinned.
		assert.Len(t, opts.IPAddresses, 4, "pinned mode must not include local interface IPs")
		assert.Contains(t, opts.DNSNames, "kubesolo.local", "extra SAN DNS name must be present")
	})
}
