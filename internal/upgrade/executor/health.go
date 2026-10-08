package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/pkg/components/coredns"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Health check names, as they appear in upgrade.Health.Checks.
const (
	CheckAPIServer = "apiserver"
	CheckNode      = "node"
	CheckVersion   = "version"
	CheckDNS       = "coredns"
)

// CheckHealth is KubeSolo's own view of whether it works: the API server is
// ready, the node is Ready, and CoreDNS — the first workload KubeSolo deploys,
// and so a fair test that the runtime, the network and the kubelet all work —
// has a ready pod.
//
// wantVersion, when set, must be the KubeSolo version the kubelet reports. The
// node object outlives the process, so a Ready node alone does not show which
// binary is behind it.
func CheckHealth(ctx context.Context, kubeconfig, wantVersion string) upgrade.Health {
	h := upgrade.Health{Checks: map[string]string{}}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := newClient(kubeconfig)
	if err != nil {
		h.Checks[CheckAPIServer] = err.Error()
		return h
	}

	if raw, err := client.Discovery().RESTClient().Get().AbsPath("/readyz").DoRaw(ctx); err != nil {
		h.Checks[CheckAPIServer] = fmt.Sprintf("not ready: %v", err)
		return h
	} else if strings.TrimSpace(string(raw)) != "ok" {
		h.Checks[CheckAPIServer] = "not ready: " + strings.TrimSpace(string(raw))
		return h
	}
	h.Checks[CheckAPIServer] = "ok"

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	switch {
	case err != nil:
		h.Checks[CheckNode] = err.Error()
	case len(nodes.Items) == 0:
		h.Checks[CheckNode] = "no node registered"
	default:
		// KubeSolo is single-node, but a node left behind by an old hostname may
		// still be listed. The freshest heartbeat is this one.
		node := newestNode(nodes.Items)
		if ready, reason := nodeReady(node); ready {
			h.Checks[CheckNode] = "ok"
		} else {
			h.Checks[CheckNode] = fmt.Sprintf("%s is not Ready: %s", node.Name, reason)
		}
		if wantVersion != "" {
			kv := node.Status.NodeInfo.KubeletVersion
			if strings.HasSuffix(kv, "+kubesolo-"+wantVersion) {
				h.Checks[CheckVersion] = "ok"
			} else {
				h.Checks[CheckVersion] = fmt.Sprintf("the kubelet reports %s, waiting for kubesolo-%s", kv, wantVersion)
			}
		}
	}

	pods, err := client.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: coredns.PodSelector})
	switch {
	case err != nil:
		h.Checks[CheckDNS] = err.Error()
	case anyPodReady(pods.Items):
		h.Checks[CheckDNS] = "ok"
	default:
		h.Checks[CheckDNS] = "no ready CoreDNS pod"
	}

	h.Healthy = true
	for _, v := range h.Checks {
		if v != "ok" {
			h.Healthy = false
		}
	}
	return h
}

// Summary describes the failing checks in one line.
func Summary(h upgrade.Health) string {
	var failing []string
	for _, name := range []string{CheckAPIServer, CheckNode, CheckVersion, CheckDNS} {
		if v, ok := h.Checks[name]; ok && v != "ok" {
			failing = append(failing, name+": "+v)
		}
	}
	if len(failing) == 0 {
		return "healthy"
	}
	return strings.Join(failing, "; ")
}

func newClient(kubeconfig string) (*kubernetes.Clientset, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", kubeconfig, err)
	}
	cfg.Timeout = 10 * time.Second
	return kubernetes.NewForConfig(cfg)
}

func newestNode(nodes []corev1.Node) corev1.Node {
	best := nodes[0]
	for _, n := range nodes[1:] {
		if heartbeat(n).After(heartbeat(best)) {
			best = n
		}
	}
	return best
}

func heartbeat(n corev1.Node) time.Time {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.LastHeartbeatTime.Time
		}
	}
	return time.Time{}
}

func nodeReady(n corev1.Node) (bool, string) {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue, c.Reason
		}
	}
	return false, "no Ready condition"
}

func anyPodReady(pods []corev1.Pod) bool {
	for _, p := range pods {
		if p.DeletionTimestamp != nil {
			continue
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}
