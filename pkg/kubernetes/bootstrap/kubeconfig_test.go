package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	bootstraputil "k8s.io/cluster-bootstrap/token/util"
)

// write puts contents in a temp file and returns its path.
func write(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "bootstrap-kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))

	return path
}

// talosBootstrapKubeconfig is the shape Talos writes to
// /etc/kubernetes/bootstrap-kubeconfig, which is the file this exists to read.
const talosBootstrapKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: https://localhost:7445
contexts:
- name: local@local
  context:
    cluster: local
    user: kubelet-bootstrap
current-context: local@local
users:
- name: kubelet-bootstrap
  user:
    token: abcdef.0123456789abcdef
`

func TestTokenFromKubeconfig(t *testing.T) {
	token, err := TokenFromKubeconfig(write(t, talosBootstrapKubeconfig))
	require.NoError(t, err)
	require.Equal(t, "abcdef.0123456789abcdef", token)
}

// The current context decides which credential is used, so a kubeconfig holding
// more than one must not be read positionally.
func TestTokenFromKubeconfigFollowsTheCurrentContext(t *testing.T) {
	const twoUsers = `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: https://localhost:7445
contexts:
- name: other@local
  context:
    cluster: local
    user: other
- name: local@local
  context:
    cluster: local
    user: kubelet-bootstrap
current-context: local@local
users:
- name: other
  user:
    token: aaaaaa.1111111111111111
- name: kubelet-bootstrap
  user:
    token: abcdef.0123456789abcdef
`

	token, err := TokenFromKubeconfig(write(t, twoUsers))
	require.NoError(t, err)
	require.Equal(t, "abcdef.0123456789abcdef", token)
}

// Each of these otherwise surfaces as a 401 at the kubelet, with nothing
// pointing at the kubeconfig as the cause.
func TestTokenFromKubeconfigRejects(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := TokenFromKubeconfig("/nonexistent/bootstrap-kubeconfig")
		require.ErrorContains(t, err, "failed to read bootstrap kubeconfig")
	})

	t.Run("no such context", func(t *testing.T) {
		const noContext = `apiVersion: v1
kind: Config
current-context: missing
`
		_, err := TokenFromKubeconfig(write(t, noContext))
		require.ErrorContains(t, err, "no context named")
	})

	// An already-enrolled kubelet's kubeconfig, which is the easiest wrong file
	// to point at: it sits in the same directory under a similar name.
	t.Run("certificate instead of a token", func(t *testing.T) {
		const certOnly = `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: https://localhost:7445
contexts:
- name: local@local
  context:
    cluster: local
    user: kubelet
current-context: local@local
users:
- name: kubelet
  user:
    client-certificate: /var/lib/kubelet/pki/kubelet-client-current.pem
    client-key: /var/lib/kubelet/pki/kubelet-client-current.pem
`
		_, err := TokenFromKubeconfig(write(t, certOnly))
		require.ErrorContains(t, err, "carries no token")
	})
}

// The API server rejects a bootstrap token Secret whose auth-extra-groups do
// not match its own pattern, and the rejection names the Secret rather than
// the group. Upstream owns the rule, so ask it.
func TestTokenGroupIsAValidBootstrapGroup(t *testing.T) {
	require.NoError(t, bootstraputil.ValidateBootstrapGroupName(tokenGroup))
}
