package network

import (
	"net"
	"strconv"
)

// LoopbackURL is the URL of path on a component listening on loopback at port,
// as KubeSolo reaches its own components for health checks.
func LoopbackURL(scheme string, port int, path string) string {
	return scheme + "://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + path
}
