package pki

import (
	"net"
	"os"
	"path/filepath"
	"strings"
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
			NodeIP:              "10.130.0.5",
			NodeIPSpecified:     true,
			APIServerExtraSANs:  []string{"1.2.3.4", "kubesolo.local"},
			KubernetesServiceIP: types.DefaultKubernetesServiceIP,
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

func TestResolvConfDomain(t *testing.T) {
	for _, tc := range []struct {
		name, conf, want string
	}{
		{"domain line, as Apple container writes it", "nameserver 192.168.64.1\ndomain test\n", "test"},
		{"no domain line", "nameserver 192.168.64.1\n", ""},
		{"search lines are ignored", "nameserver 10.0.0.1\nsearch corp.example lab.example\n", ""},
		{"trailing dot and case", "domain Corp.Example.\n", "corp.example"},
		{"the last domain line wins", "domain one.example\ndomain two.example\n", "two.example"},
		{"comments", "# domain commented.example\n; domain also.example\ndomain real.example # note\n", "real.example"},
		{"invalid domain", "domain under_score\n", ""},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolvConfDomain([]byte(tc.conf)))
		})
	}
}

func TestDefaultCertOptions_APIServerNodeDNSName(t *testing.T) {
	setResolvConf := func(t *testing.T, conf string) {
		path := filepath.Join(t.TempDir(), "resolv.conf")
		if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		old := resolvConfPath
		resolvConfPath = path
		t.Cleanup(func() { resolvConfPath = old })
	}
	base := []string{
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster",
		"kubernetes.default.svc.cluster.local",
		"localhost",
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	nodeDNSName := strings.ToLower(hostname) + ".test"

	t.Run("container mode with a domain line adds <hostname>.<domain>", func(t *testing.T) {
		setResolvConf(t, "nameserver 192.168.64.1\ndomain test\n")
		opts := defaultCertOptions(APIServerCert, types.Embedded{ContainerMode: true})
		assert.Equal(t, append(base, nodeDNSName), opts.DNSNames)
	})
	t.Run("container mode without a domain line is unchanged", func(t *testing.T) {
		setResolvConf(t, "nameserver 192.168.64.1\nsearch test\n")
		opts := defaultCertOptions(APIServerCert, types.Embedded{ContainerMode: true})
		assert.Equal(t, base, opts.DNSNames)
	})
	t.Run("container mode without a resolv.conf is unchanged", func(t *testing.T) {
		old := resolvConfPath
		resolvConfPath = filepath.Join(t.TempDir(), "missing")
		t.Cleanup(func() { resolvConfPath = old })
		opts := defaultCertOptions(APIServerCert, types.Embedded{ContainerMode: true})
		assert.Equal(t, base, opts.DNSNames)
	})
	t.Run("host mode ignores the domain line", func(t *testing.T) {
		setResolvConf(t, "nameserver 192.168.64.1\ndomain test\n")
		opts := defaultCertOptions(APIServerCert, types.Embedded{})
		assert.Equal(t, base, opts.DNSNames)
	})
	t.Run("only the API server certificate gets the name", func(t *testing.T) {
		setResolvConf(t, "nameserver 192.168.64.1\ndomain test\n")
		opts := defaultCertOptions(KubeletCert, types.Embedded{ContainerMode: true, NodeName: "node"})
		assert.NotContains(t, opts.DNSNames, nodeDNSName)
	})
}
