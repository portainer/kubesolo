package network

import (
	"net"
	"testing"

	"github.com/portainer/kubesolo/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectMTU(t *testing.T) {
	t.Run("prefers the MTU of a private-IP interface over a public one, regardless of order", func(t *testing.T) {
		candidates := []ifaceCandidate{
			{mtu: 1500, addrs: []net.Addr{cidr(t, "203.0.113.9/24")}}, // public, listed first
			{mtu: 1400, addrs: []net.Addr{cidr(t, "192.168.1.10/24")}},
		}
		got, err := selectMTU(candidates)
		require.NoError(t, err)
		assert.Equal(t, 1400, got)
	})

	t.Run("falls back to the MTU of a public-IP interface when no private address exists", func(t *testing.T) {
		candidates := []ifaceCandidate{
			{mtu: 65536, addrs: []net.Addr{cidr(t, "127.0.0.1/8")}}, // loopback, ignored
			{mtu: 1450, addrs: []net.Addr{cidr(t, "203.0.113.9/24")}},
		}
		got, err := selectMTU(candidates)
		require.NoError(t, err)
		assert.Equal(t, 1450, got)
	})

	t.Run("skips a candidate with a non-positive MTU", func(t *testing.T) {
		candidates := []ifaceCandidate{
			{mtu: 0, addrs: []net.Addr{cidr(t, "192.168.1.10/24")}},
			{mtu: 1500, addrs: []net.Addr{cidr(t, "192.168.1.11/24")}},
		}
		got, err := selectMTU(candidates)
		require.NoError(t, err)
		assert.Equal(t, 1500, got)
	})

	t.Run("errors and returns the default MTU when only loopback is present", func(t *testing.T) {
		candidates := []ifaceCandidate{
			{mtu: 65536, addrs: []net.Addr{cidr(t, "127.0.0.1/8")}},
		}
		got, err := selectMTU(candidates)
		require.Error(t, err)
		assert.Equal(t, types.DefaultMTU, got)
	})
}

func TestResolveMTU(t *testing.T) {
	t.Run("zero or negative override means auto-detect and not pinned", func(t *testing.T) {
		_, pinned, _ := ResolveMTU(0)
		assert.False(t, pinned)

		_, pinned, _ = ResolveMTU(-1)
		assert.False(t, pinned)
	})

	t.Run("valid override is used as-is and marked pinned", func(t *testing.T) {
		got, pinned, err := ResolveMTU(1400)
		require.NoError(t, err)
		assert.True(t, pinned, "a valid override must be reported as pinned")
		assert.Equal(t, 1400, got)
	})

	t.Run("override outside the valid range falls back to auto-detection and is not pinned", func(t *testing.T) {
		got, pinned, _ := ResolveMTU(70000)
		assert.False(t, pinned, "an out-of-range override must not be treated as pinned")
		assert.NotEqual(t, 70000, got)
	})
}
