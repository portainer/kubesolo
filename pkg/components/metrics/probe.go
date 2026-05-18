package metrics

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// prober is a small function type used to validate one component's health.
// It returns nil on success and a non-nil error otherwise. Errors are not
// surfaced to scrapers — they only flip the component_up gauge to 0 and are
// logged at debug level.
type prober func(ctx context.Context) error

// componentNames is the canonical list of components that the metrics service
// reports an "up" gauge for. Channels in componentReady use the same keys.
const (
	ComponentRuntime    = "runtime"
	ComponentKine       = "kine"
	ComponentAPIServer  = "apiserver"
	ComponentController = "controller"
	ComponentKubelet    = "kubelet"
	ComponentKubeProxy  = "kubeproxy"
	ComponentCoreDNS    = "coredns"
	ComponentWebhook    = "webhook"
)

// buildProbers wires the per-component health probes. The set is built once
// at startup so that things like the lazily-initialised Kubernetes client for
// the coredns probe are shared across ticks.
//
// kube-proxy is intentionally absent: it runs in-process with metrics-bind-address
// disabled (see pkg/kubernetes/kubeproxy/flags.go) and exposes no healthz socket,
// so the only honest signal we have is its readiness channel. The watcher in
// watchReadyChannels handles that, which is why component_up{component="kubeproxy"}
// stays at 0 until the readiness channel closes.
func (s *Service) buildProbers() map[string]prober {
	httpClient := newProbeHTTPClient()
	tlsClient := newProbeTLSClient()

	clientCache := newK8sClientCache(s.embedded.AdminKubeconfigFile)

	return map[string]prober{
		ComponentRuntime:    probeFileExists(s.embedded.RuntimeSocketPath),
		ComponentKine:       probeTCP(types.DefaultKineEndpoint),
		ComponentAPIServer:  probeHTTP(tlsClient, "https://127.0.0.1:6443/livez"),
		ComponentController: probeHTTP(tlsClient, "https://127.0.0.1:10257/healthz"),
		ComponentKubelet:    probeHTTP(httpClient, "http://127.0.0.1:10248/healthz"),
		ComponentWebhook:    probeTCP(fmt.Sprintf("127.0.0.1:%d", types.DefaultWebhookPort)),
		ComponentCoreDNS:    probeCoreDNS(clientCache),
	}
}

// newProbeHTTPClient returns a small http.Client suitable for plain-HTTP
// probes against loopback. It deliberately uses a short timeout to avoid
// stalling the prober loop on a misbehaving component.
func newProbeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives:   true,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 1,
		},
	}
}

// newProbeTLSClient returns an http.Client that skips TLS verification.
// Probes only ever target loopback control plane endpoints whose serving
// certificates are signed by the cluster's internal CA — verifying them
// here would require wiring the CA bundle into the metrics service for no
// real security benefit since the metrics service is already in-process.
func newProbeTLSClient() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives:   true,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 1,
		},
	}
}

// probeHTTP returns a prober that GETs the given URL and expects a 2xx response.
func probeHTTP(client *http.Client, url string) prober {
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		return nil
	}
}

// probeTCP returns a prober that opens a TCP connection to the given address.
// It is used for components that don't expose an HTTP healthz endpoint, such
// as kine (which speaks the etcd gRPC protocol).
func probeTCP(addr string) prober {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return func(ctx context.Context) error {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	}
}

// probeFileExists returns a prober that checks whether the given file/socket
// exists on disk. It is used for the CRI unix socket — if the socket is
// absent, the container runtime has either not started yet or has crashed.
func probeFileExists(path string) prober {
	return func(_ context.Context) error {
		if _, err := os.Stat(path); err != nil {
			return err
		}
		return nil
	}
}

// k8sClientCache lazily constructs a Kubernetes clientset using the admin
// kubeconfig and caches it for subsequent probes. Calls before the apiserver
// is ready will fail fast and the next tick will retry.
type k8sClientCache struct {
	mu         sync.Mutex
	kubeconfig string
	client     *kubernetes.Clientset
}

func newK8sClientCache(kubeconfig string) *k8sClientCache {
	return &k8sClientCache{kubeconfig: kubeconfig}
}

func (c *k8sClientCache) get() (*kubernetes.Clientset, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return c.client, nil
	}
	cs, err := kubesolokubernetes.GetKubernetesClient(c.kubeconfig)
	if err != nil {
		return nil, err
	}
	c.client = cs
	return cs, nil
}

// probeCoreDNS reports CoreDNS as up when its Deployment in kube-system has
// at least one Available replica. CoreDNS runs as an in-cluster pod, so this
// check is necessarily indirect (and depends on the apiserver being healthy).
func probeCoreDNS(cache *k8sClientCache) prober {
	return func(ctx context.Context) error {
		cs, err := cache.get()
		if err != nil {
			return fmt.Errorf("kube client: %w", err)
		}

		dep, err := cs.AppsV1().Deployments("kube-system").Get(ctx, "coredns", metav1.GetOptions{})
		if err != nil {
			return err
		}
		if dep.Status.AvailableReplicas < 1 {
			return fmt.Errorf("no available coredns replicas")
		}
		return nil
	}
}
