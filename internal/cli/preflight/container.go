package preflight

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// ContainerSuite returns pre-flight checks for container run mode (macOS/Docker).
// Only verifies the Docker daemon is reachable — port conflicts are surfaced by
// Docker itself when the container starts.
func ContainerSuite() []Check {
	return []Check{
		{Name: "Docker daemon", Run: CheckDockerDaemon},
	}
}

// CheckDockerDaemon verifies the Docker daemon socket is reachable.
func CheckDockerDaemon() error {
	socket := "/var/run/docker.sock"
	if h := os.Getenv("DOCKER_HOST"); strings.HasPrefix(h, "unix://") {
		socket = strings.TrimPrefix(h, "unix://")
	}
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return fmt.Errorf("Docker daemon not accessible at %s: %w (is Docker Desktop running?)", socket, err)
	}
	conn.Close()
	return nil
}
