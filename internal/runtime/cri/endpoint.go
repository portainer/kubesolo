// Package cri resolves the CRI endpoint KubeSolo talks to: either a
// host-managed container runtime named with --container-runtime-endpoint, or
// KubeSolo's own embedded containerd.
package cri

import (
	"fmt"
	"strings"
)

// unixScheme is the only CRI endpoint scheme the kubelet supports on Linux.
const unixScheme = "unix://"

// Endpoint is the resolved CRI endpoint.
type Endpoint struct {
	// URL is the endpoint in unix:// form, as passed to the kubelet and the CRI client.
	URL string
	// SocketPath is the filesystem path of URL, for existence checks.
	SocketPath string
	// External is true when the endpoint belongs to a host-managed runtime
	// rather than the containerd KubeSolo starts itself.
	External bool
}

// Resolve parses a --container-runtime-endpoint value. A bare path is accepted
// and given the unix:// scheme, since runtime documentation commonly quotes it
// that way. An empty value yields the zero Endpoint: the caller then supplies
// the embedded containerd endpoint with Embedded, which depends on --path.
func Resolve(flag string) (Endpoint, error) {
	value := strings.TrimSpace(flag)
	if value == "" {
		return Endpoint{}, nil
	}

	socketPath := strings.TrimPrefix(value, unixScheme)
	if !strings.HasPrefix(socketPath, "/") {
		return Endpoint{}, fmt.Errorf("invalid container runtime endpoint %q: expected an absolute socket path or a unix:// URL, for example unix:///run/crio/crio.sock", value)
	}

	return Endpoint{URL: unixScheme + socketPath, SocketPath: socketPath, External: true}, nil
}

// Embedded returns the endpoint of the containerd KubeSolo runs itself.
func Embedded(socketPath string) Endpoint {
	return Endpoint{URL: unixScheme + socketPath, SocketPath: socketPath}
}
