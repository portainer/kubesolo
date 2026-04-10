package coredns

import "fmt"

// dnsBoundPort is the port CoreDNS listens on. A non-privileged port above 1024
// avoids conflicts with systemd-resolved (127.0.0.53:53) and does not require
// CAP_NET_BIND_SERVICE. kube-proxy DNATs ClusterIP:53 → nodeIP:dnsBoundPort.
const dnsBoundPort = 553

// forwardOnlyCorefile returns a Corefile bound to all interfaces with upstream forwarding only.
// Starts before the apiserver is ready; provides upstream DNS forwarding immediately.
func forwardOnlyCorefile() string {
	return fmt.Sprintf(`.:%d {
    errors
    loop
    cache 30
    forward . /etc/resolv.conf
    health :8080
    ready :8181
}`, dnsBoundPort)
}

// clusterAwareCorefile returns a full Corefile with the kubernetes plugin enabled.
// apiServerEndpoint is the HTTPS address of the apiserver (e.g. "https://127.0.0.1:6443").
// clientCert, clientKey, caCert are paths from types.Embedded PKI, ordered to match
// the CoreDNS tls directive: tls CERT KEY CA.
func clusterAwareCorefile(apiServerEndpoint, clientCert, clientKey, caCert string) string {
	return fmt.Sprintf(`.:%d {
    errors
    loop
    cache 30 {
        disable denial cluster.local
    }
    kubernetes cluster.local in-addr.arpa ip6.arpa {
        endpoint %s
        tls %s %s %s
        pods insecure
        fallthrough in-addr.arpa ip6.arpa
        ttl 30
    }
    forward . /etc/resolv.conf
    health :8080
    ready :8181
}`, dnsBoundPort, apiServerEndpoint, clientCert, clientKey, caCert)
}
