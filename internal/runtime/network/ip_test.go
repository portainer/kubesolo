package network

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cidr(t *testing.T, s string) *net.IPNet {
	t.Helper()
	ip, ipnet, err := net.ParseCIDR(s)
	require.NoError(t, err)
	ipnet.IP = ip
	return ipnet
}

func TestSelectNodeIP(t *testing.T) {
	t.Run("prefers a private IP over a public one regardless of order", func(t *testing.T) {
		addrs := []net.Addr{
			cidr(t, "203.0.113.9/24"), // public, listed first
			cidr(t, "192.168.1.10/24"),
		}
		got, err := selectNodeIP(addrs)
		require.NoError(t, err)
		assert.Equal(t, "192.168.1.10", got)
	})

	t.Run("falls back to a public IP when no private address exists", func(t *testing.T) {
		addrs := []net.Addr{
			cidr(t, "127.0.0.1/8"),
			cidr(t, "203.0.113.9/24"),
		}
		got, err := selectNodeIP(addrs)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.9", got)
	})

	t.Run("errors and returns loopback when only loopback is present", func(t *testing.T) {
		addrs := []net.Addr{cidr(t, "127.0.0.1/8")}
		got, err := selectNodeIP(addrs)
		require.Error(t, err)
		assert.Equal(t, "127.0.0.1", got)
	})
}

func TestResolveNodeIP(t *testing.T) {
	t.Run("valid override is used as-is and marked pinned", func(t *testing.T) {
		// 203.0.113.5 is a documentation-range address, not bound locally, so it
		// exercises the "valid but not local" (VIP) branch.
		got, pinned, err := ResolveNodeIP("203.0.113.5")
		require.NoError(t, err)
		assert.True(t, pinned, "a valid override must be reported as pinned")
		assert.Equal(t, "203.0.113.5", got)
	})

	t.Run("invalid override falls back to auto-detection and is not pinned", func(t *testing.T) {
		// Falls through to GetNodeIP, which is host-dependent (and errors on a
		// loopback-only host), so we only assert the override was rejected — not
		// the detected value or the error.
		got, pinned, _ := ResolveNodeIP("not-an-ip")
		assert.False(t, pinned, "an invalid override must not be treated as pinned")
		assert.NotEqual(t, "not-an-ip", got)
	})
}

func TestIsIPv4Address(t *testing.T) {
	cases := map[string]bool{
		"10.43.0.1":       true,
		"127.0.0.1":       true,
		"0.0.0.0":         true,
		"255.255.255.255": true,
		"::1":             false, // IPv6
		"2001:db8::1":     false,
		"10.43.0.256":     false, // out of range octet
		"not-an-ip":       false,
		"":                false,
		"10.43.0":         false,
	}
	for in, want := range cases {
		assert.Equalf(t, want, IsIPv4Address(in), "IsIPv4Address(%q)", in)
	}
}

func TestIsDNSName(t *testing.T) {
	cases := map[string]bool{
		"kubernetes.default":  true,
		"webhook.kubesolo.io": true,
		"a":                   true,
		"node-1":              true,
		"":                    false,
		"-leadinghyphen":      false,
		"trailinghyphen-":     false,
		"under_score":         false,
		"has space":           false,
		"label..empty":        false,
	}
	for in, want := range cases {
		assert.Equalf(t, want, IsDNSName(in), "IsDNSName(%q)", in)
	}

	// A name longer than 253 characters is rejected regardless of label validity.
	long := make([]byte, 254)
	for i := range long {
		long[i] = 'a'
	}
	assert.False(t, IsDNSName(string(long)), "name >253 chars must be rejected")
}

func TestIsValidNameserver(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":         true, // global unicast
		"1.1.1.1":         true,
		"169.254.169.254": true,  // cloud metadata IP, explicitly allowed
		"127.0.0.1":       false, // loopback
		"169.254.0.1":     false, // link-local (not the metadata IP)
		"0.0.0.0":         false, // unspecified
		"garbage":         false,
		"":                false,
	}
	for in, want := range cases {
		assert.Equalf(t, want, isValidNameserver(in), "isValidNameserver(%q)", in)
	}
}

func TestIsValidResolvConf(t *testing.T) {
	dir := t.TempDir()

	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}

	valid := write("valid", "nameserver 8.8.8.8\nsearch example.com\n")
	assert.True(t, isValidResolvConf(valid))

	// A loopback nameserver makes the whole file unusable for pods.
	loopback := write("loopback", "nameserver 127.0.0.53\n")
	assert.False(t, isValidResolvConf(loopback))

	// No nameserver line at all.
	noNS := write("none", "search example.com\noptions ndots:5\n")
	assert.False(t, isValidResolvConf(noNS))

	// Missing file.
	assert.False(t, isValidResolvConf(filepath.Join(dir, "does-not-exist")))
}

func TestSanitizeResolvConf(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}

	t.Run("under limit returns source unchanged", func(t *testing.T) {
		src := write("three", "nameserver 8.8.8.8\nnameserver 1.1.1.1\nnameserver 9.9.9.9\nsearch x\n")
		out, err := sanitizeResolvConf(src, dir)
		require.NoError(t, err)
		assert.Equal(t, src, out, "no rewrite expected at or below the limit")
	})

	t.Run("missing source returns source unchanged", func(t *testing.T) {
		missing := filepath.Join(dir, "nope")
		out, err := sanitizeResolvConf(missing, dir)
		require.NoError(t, err)
		assert.Equal(t, missing, out)
	})

	t.Run("over limit caps, dedupes and preserves other lines", func(t *testing.T) {
		dataDir := t.TempDir()
		src := write("many",
			"nameserver 8.8.8.8\n"+
				"nameserver 8.8.8.8\n"+ // duplicate, dropped
				"nameserver 1.1.1.1\n"+
				"nameserver 9.9.9.9\n"+
				"nameserver 4.4.4.4\n"+ // 4th unique, dropped by cap
				"search corp.example.com\n"+
				"options ndots:2\n")
		out, err := sanitizeResolvConf(src, dataDir)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dataDir, "resolv.conf"), out)

		b, err := os.ReadFile(out)
		require.NoError(t, err)
		got := string(b)

		assert.Contains(t, got, "nameserver 8.8.8.8\n")
		assert.Contains(t, got, "nameserver 1.1.1.1\n")
		assert.Contains(t, got, "nameserver 9.9.9.9\n")
		assert.NotContains(t, got, "4.4.4.4", "4th unique nameserver must be capped out")
		// Non-nameserver lines are preserved.
		assert.Contains(t, got, "search corp.example.com\n")
		assert.Contains(t, got, "options ndots:2\n")
		// Exactly maxNameservers nameserver lines remain (dedup + cap).
		assert.Equal(t, maxNameservers, countNameservers(got))
	})
}

func countNameservers(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "nameserver") {
			n++
		}
	}
	return n
}

func TestGetHostResolvConf_ContainerMode(t *testing.T) {
	// In container mode the host resolv.conf is never consulted; pods get
	// /dev/null to prevent host DNS leakage.
	assert.Equal(t, "/dev/null", GetHostResolvConf(t.TempDir(), true))
}

func TestResolveLoadBalancerIP(t *testing.T) {
	t.Run("falls back to the node IP when unset", func(t *testing.T) {
		assert.Equal(t, "192.168.1.10", ResolveLoadBalancerIP("", "192.168.1.10"))
	})

	t.Run("falls back to the node IP when not a valid IPv4 address", func(t *testing.T) {
		// Must not re-detect: a pinned --node-ip has to win over a bad override.
		assert.Equal(t, "192.168.1.10", ResolveLoadBalancerIP("not-an-ip", "192.168.1.10"))
		assert.Equal(t, "192.168.1.10", ResolveLoadBalancerIP("2001:db8::1", "192.168.1.10"))
	})

	t.Run("uses a valid override bound to a local interface", func(t *testing.T) {
		// GetLocalIPs always appends 127.0.0.1, so this is local on any host.
		assert.Equal(t, "127.0.0.1", ResolveLoadBalancerIP("127.0.0.1", "192.168.1.10"))
	})

	t.Run("uses a valid override that is not bound locally (e.g. VIP)", func(t *testing.T) {
		assert.Equal(t, "203.0.113.9", ResolveLoadBalancerIP("203.0.113.9", "192.168.1.10"))
	})
}
