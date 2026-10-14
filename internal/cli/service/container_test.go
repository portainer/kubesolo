package service

import (
	"strconv"
	"strings"
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

func TestProxyEnv(t *testing.T) {
	if env := proxyEnv("", []string{"172.20.0.0/16"}); env != nil {
		t.Errorf("no proxy: %v", env)
	}
	env := proxyEnv("http://proxy:3128", []string{"172.20.0.0/16"})
	want := []string{
		"HTTP_PROXY=http://proxy:3128",
		"HTTPS_PROXY=http://proxy:3128",
		"NO_PROXY=localhost,127.0.0.1,172.20.0.0/16,10.42.0.0/16,10.43.0.0/16,.svc,.cluster.local",
	}
	if strings.Join(env, " ") != strings.Join(want, " ") {
		t.Errorf("env = %v\nwant  %v", env, want)
	}
}

// An upgrade passes a config without --proxy; the previous container's proxy
// carries over unless a new one is given.
func TestProxyFor(t *testing.T) {
	prev := &container.InspectResponse{Config: &container.Config{Env: []string{
		"PATH=/usr/bin", "HTTP_PROXY=http://old:3128", "NO_PROXY=localhost",
	}}}
	if got := proxyFor(&config.Config{}, prev); got != "http://old:3128" {
		t.Errorf("carried over = %q", got)
	}
	if got := proxyFor(&config.Config{Proxy: "http://new:3128"}, prev); got != "http://new:3128" {
		t.Errorf("explicit = %q", got)
	}
	if got := proxyFor(&config.Config{}, nil); got != "" {
		t.Errorf("first install = %q", got)
	}
}
