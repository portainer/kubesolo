package kubeconfig

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// WaitForNodeReady polls the API server at addr (host:port) with the admin
// credentials in the kubeconfig kc until the node reports Ready, or timeout
// expires. An API server that answers is not yet a working node: the kubelet
// can still fail to register or to run.
func WaitForNodeReady(kc []byte, addr string, timeout time.Duration) error {
	httpClient, err := adminClient(kc)
	if err != nil {
		return err
	}

	log.Info().Msgf("waiting for the node to be Ready (up to %s)...", timeout.Round(time.Second))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready, _ := nodeReady(httpClient, addr); ready {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("the node did not become Ready within %s", timeout.Round(time.Second))
}

// NodeReady asks the API server at addr (host:port), once, whether the node is
// Ready, with the admin credentials in the kubeconfig kc.
func NodeReady(kc []byte, addr string) (bool, error) {
	httpClient, err := adminClient(kc)
	if err != nil {
		return false, err
	}
	return nodeReady(httpClient, addr)
}

func nodeReady(httpClient *http.Client, addr string) (bool, error) {
	resp, err := httpClient.Get("https://" + addr + "/api/v1/nodes")
	if err != nil {
		return false, err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("the API server answered %s", resp.Status)
	}
	return nodesReady(body), nil
}

// nodesReady reports whether a node list holds at least one node, and every
// node in it is Ready.
func nodesReady(body []byte) bool {
	var list struct {
		Items []struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &list) != nil || len(list.Items) == 0 {
		return false
	}
	for _, n := range list.Items {
		ready := false
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}
		if !ready {
			return false
		}
	}
	return true
}

// adminClient returns an HTTP client that authenticates with the client
// certificate of the KubeSolo admin kubeconfig kc and trusts its CA.
func adminClient(kc []byte) (*http.Client, error) {
	doc, err := parseDoc(kc)
	if err != nil {
		return nil, err
	}
	field := func(entry map[string]any, section, key string) ([]byte, error) {
		m, _ := entry[section].(map[string]any)
		v, _ := m[key].(string)
		return base64.StdEncoding.DecodeString(v)
	}
	// The user with a client certificate; the kubeconfig also holds a token user.
	var user map[string]any
	for _, u := range namedList(doc, "users") {
		if cert, _ := field(u, "user", "client-certificate-data"); len(cert) > 0 {
			user = u
		}
	}
	clusters := namedList(doc, "clusters")
	if len(clusters) == 0 || user == nil {
		return nil, fmt.Errorf("the kubeconfig has no cluster or no user with a client certificate")
	}
	ca, err := field(clusters[0], "cluster", "certificate-authority-data")
	if err != nil {
		return nil, err
	}
	cert, err := field(user, "user", "client-certificate-data")
	if err != nil {
		return nil, err
	}
	key, err := field(user, "user", "client-key-data")
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("invalid admin credentials in the kubeconfig: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("invalid CA in the kubeconfig")
	}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:      pool,
			Certificates: []tls.Certificate{pair},
			MinVersion:   tls.VersionTLS12,
		}},
	}, nil
}
