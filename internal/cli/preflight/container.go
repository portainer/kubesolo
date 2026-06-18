package preflight

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// ContainerSuite returns pre-flight checks for container run mode (macOS,
// Windows WSL2, or Linux). Only verifies the container engine is reachable —
// port conflicts are surfaced by the engine itself when the container starts.
func ContainerSuite() []Check {
	return []Check{
		{Name: "Container engine", Run: CheckContainerEngine},
	}
}

// CheckContainerEngine verifies the container engine socket is reachable.
func CheckContainerEngine() error {
	socket := "/var/run/docker.sock"
	if h := os.Getenv("DOCKER_HOST"); strings.HasPrefix(h, "unix://") {
		socket = strings.TrimPrefix(h, "unix://")
	}
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return fmt.Errorf("container engine not accessible at %s: %w (is the container engine running?)", socket, err)
	}
	conn.Close()
	return nil
}
