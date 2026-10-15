package preflight

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/portainer/kubesolo/internal/cli/dockerhost"
)

// ContainerSuite returns pre-flight checks for container run mode (macOS,
// Windows WSL2, or Linux). Only verifies the container engine is reachable —
// port conflicts are surfaced by the engine itself when the container starts.
func ContainerSuite() []Check {
	return []Check{
		{Name: "Container engine", Run: CheckContainerEngine},
	}
}

// CheckContainerEngine verifies the container engine is reachable. It resolves
// the engine as the Docker client does — DOCKER_HOST, else the active Docker
// context — so it checks what kubesoloctl actually connects to: a unix socket
// by default, or a TCP endpoint. Schemes it cannot cheaply probe (ssh://,
// npipe://, …) are left to the client at container-create time rather than
// failed here.
func CheckContainerEngine() error {
	host, err := dockerhost.Host()
	if err != nil {
		return err
	}
	switch {
	case host == "":
		return dialEngine("unix", "/var/run/docker.sock")
	case strings.HasPrefix(host, "unix://"):
		return dialEngine("unix", strings.TrimPrefix(host, "unix://"))
	case strings.HasPrefix(host, "tcp://"):
		return dialEngine("tcp", strings.TrimPrefix(host, "tcp://"))
	default:
		return nil
	}
}

// dialEngine attempts a short-lived connection to the engine endpoint, returning
// a descriptive error if it is unreachable.
func dialEngine(network, addr string) error {
	conn, err := net.DialTimeout(network, addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("container engine not accessible at %s://%s: %w (is the container engine running?)", network, addr, err)
	}
	_ = conn.Close()
	return nil
}
