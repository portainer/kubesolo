package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testNamespace = "tier6-lb"
	testService   = "flip-me"
	testNodeIP    = "10.10.217.5"
)

func lbTestService(svcType corev1.ServiceType) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: testService, Namespace: testNamespace},
		Spec:       corev1.ServiceSpec{Type: svcType},
	}
}

// statusPatched reports whether a patch was issued against the status subresource.
func statusPatched(actions []k8stesting.Action) bool {
	for _, a := range actions {
		if a.GetVerb() == "patch" && a.GetResource().Resource == "services" && a.GetSubresource() == "status" {
			return true
		}
	}
	return false
}

func TestUpdateLoadBalancerStatusWithRetry(t *testing.T) {
	// The webhook runs at admission, which is before the object is persisted, so
	// on the UPDATE path the first Get can still return the pre-update object
	// with its old type. Treating that as "not a LoadBalancer, nothing to do"
	// left EXTERNAL-IP at <pending> forever, because nothing else retries [KS-75].
	t.Run("retries a stale pre-commit read until the LoadBalancer type is visible", func(t *testing.T) {
		cs := fake.NewClientset(lbTestService(corev1.ServiceTypeClusterIP))
		gets := 0
		cs.PrependReactor("get", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
			gets++
			if gets == 1 {
				// The stale read the UPDATE path actually sees.
				return true, lbTestService(corev1.ServiceTypeClusterIP), nil
			}
			return true, lbTestService(corev1.ServiceTypeLoadBalancer), nil
		})

		w := NewService("node-1", testNodeIP, "", "", true)
		require.NoError(t, w.updateLoadBalancerStatusWithRetry(context.Background(), testNamespace, testService, cs))

		assert.GreaterOrEqual(t, gets, 2, "expected the stale read to be retried")
		assert.True(t, statusPatched(cs.Actions()), "expected a status patch once the type was committed")
	})

	t.Run("patches immediately when the type is already committed", func(t *testing.T) {
		cs := fake.NewClientset(lbTestService(corev1.ServiceTypeLoadBalancer))

		w := NewService("node-1", testNodeIP, "", "", true)
		require.NoError(t, w.updateLoadBalancerStatusWithRetry(context.Background(), testNamespace, testService, cs))

		assert.True(t, statusPatched(cs.Actions()), "expected a status patch on the CREATE path")
	})

	t.Run("is a no-op when the EXTERNAL-IP is already correct", func(t *testing.T) {
		svc := lbTestService(corev1.ServiceTypeLoadBalancer)
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: testNodeIP}}
		cs := fake.NewClientset(svc)

		w := NewService("node-1", testNodeIP, "", "", true)
		require.NoError(t, w.updateLoadBalancerStatusWithRetry(context.Background(), testNamespace, testService, cs))

		assert.False(t, statusPatched(cs.Actions()), "should not re-patch an already-correct status")
	})
}
