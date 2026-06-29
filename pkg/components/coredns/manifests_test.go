package coredns

import (
	"context"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCreateDeployment_HostModeHasMemoryLimit(t *testing.T) {
	cs := fake.NewSimpleClientset()
	require.NoError(t, createDeployment(context.Background(), cs, false))

	dep, err := cs.AppsV1().Deployments(coreDNSNamespace).Get(context.Background(), coreDNSDeploymentName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, int32(1), *dep.Spec.Replicas)
	c := dep.Spec.Template.Spec.Containers[0]
	assert.Equal(t, types.DefaultCoreDNSImage, c.Image)

	limit := c.Resources.Limits[corev1.ResourceMemory]
	assert.Equal(t, "64Mi", limit.String(), "host mode must cap CoreDNS memory")
}

func TestCreateDeployment_ContainerModeHasNoLimit(t *testing.T) {
	cs := fake.NewSimpleClientset()
	require.NoError(t, createDeployment(context.Background(), cs, true))

	dep, err := cs.AppsV1().Deployments(coreDNSNamespace).Get(context.Background(), coreDNSDeploymentName, metav1.GetOptions{})
	require.NoError(t, err)

	c := dep.Spec.Template.Spec.Containers[0]
	assert.Empty(t, c.Resources.Limits, "container mode must not set memory limits (cgroup v2 manages it)")
	// Requests are still set in both modes.
	assert.False(t, c.Resources.Requests.Memory().IsZero())
}

func TestCreateDeployment_Idempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx := context.Background()
	require.NoError(t, createDeployment(ctx, cs, false))
	// A second call exercises the AlreadyExists -> Update path.
	require.NoError(t, createDeployment(ctx, cs, false))
}

func TestCreateService(t *testing.T) {
	cs := fake.NewSimpleClientset()
	require.NoError(t, createService(context.Background(), cs))

	svc, err := cs.CoreV1().Services(coreDNSNamespace).Get(context.Background(), coreDNSServiceName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, types.DefaultCoreDNSIP, svc.Spec.ClusterIP)

	ports := map[corev1.Protocol]int32{}
	for _, p := range svc.Spec.Ports {
		ports[p.Protocol] = p.Port
	}
	assert.Equal(t, int32(53), ports[corev1.ProtocolUDP])
	assert.Equal(t, int32(53), ports[corev1.ProtocolTCP])
}

func TestCreateConfigMap_StoresCorefile(t *testing.T) {
	cs := fake.NewSimpleClientset()
	require.NoError(t, createConfigMap(context.Background(), cs, false, false))

	cm, err := cs.CoreV1().ConfigMaps(coreDNSNamespace).Get(context.Background(), coreDNSConfigMapName, metav1.GetOptions{})
	require.NoError(t, err)

	var combined string
	for _, v := range cm.Data {
		combined += v
	}
	assert.True(t, strings.Contains(combined, "kubernetes cluster.local"),
		"ConfigMap must carry a Corefile with the kubernetes plugin")
}

func TestCreateRBAC(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx := context.Background()
	require.NoError(t, createServiceAccount(ctx, cs))
	require.NoError(t, createClusterRole(ctx, cs))
	require.NoError(t, createClusterRoleBinding(ctx, cs))

	_, err := cs.CoreV1().ServiceAccounts(coreDNSNamespace).Get(ctx, coreDNSServiceAccountName, metav1.GetOptions{})
	assert.NoError(t, err)
	_, err = cs.RbacV1().ClusterRoles().Get(ctx, coreDNSClusterRoleName, metav1.GetOptions{})
	assert.NoError(t, err)
}
