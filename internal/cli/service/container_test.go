package service

import (
	"strconv"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/portainer/kubesolo/internal/cli/config"
)

// An upgrade re-creates the container from the previous command line and a
// fresh config that has no --d2k of its own, so the command line decides.
func TestPublishD2K(t *testing.T) {
	cases := []struct {
		name string
		d2k  bool
		args []string
		want bool
	}{
		{"install with --d2k", true, []string{"--path=/var/lib/kubesolo", "--d2k"}, true},
		{"upgrade of a d2k container", false, []string{"--path=/var/lib/kubesolo", "--d2k", "--d2k-namespace=d2k"}, true},
		{"no d2k", false, []string{"--path=/var/lib/kubesolo"}, false},
		{"d2k namespace alone", false, []string{"--d2k-namespace=d2k"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := publishD2K(&config.Config{D2K: c.d2k}, c.args); got != c.want {
				t.Errorf("publishD2K = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHostPortFor(t *testing.T) {
	const api = nat.Port("6443/tcp")

	// A container created by an older kubesoloctl asked for any port; the one
	// Docker chose is what the kubeconfig points at.
	running := &container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			HostConfig: &container.HostConfig{PortBindings: nat.PortMap{api: {{HostIP: "127.0.0.1"}}}},
		},
		NetworkSettings: &container.NetworkSettings{},
	}
	running.NetworkSettings.Ports = nat.PortMap{api: {{HostIP: "127.0.0.1", HostPort: "32790"}}}
	if got, err := hostPortFor(running, api); err != nil || got != "32790" {
		t.Errorf("running container: %q, %v", got, err)
	}

	// A stopped container has no live bindings, but a pinned one recorded its port.
	stopped := &container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			HostConfig: &container.HostConfig{PortBindings: nat.PortMap{api: {{HostIP: "127.0.0.1", HostPort: "41000"}}}},
		},
		NetworkSettings: &container.NetworkSettings{},
	}
	if got, err := hostPortFor(stopped, api); err != nil || got != "41000" {
		t.Errorf("stopped container: %q, %v", got, err)
	}

	// Nothing to keep: a free port, which must be a concrete number.
	for _, prev := range []*container.InspectResponse{nil, stopped} {
		got, err := hostPortFor(prev, "2376/tcp")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := strconv.Atoi(got); err != nil || n <= 0 {
			t.Errorf("free port = %q", got)
		}
	}
}
