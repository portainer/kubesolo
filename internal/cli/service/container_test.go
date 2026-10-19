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
// fresh config that has no --d2k of its own. The command line names d2k only
// for a container configured by flags; one with a configuration file published
// the port before.
func TestPublishD2K(t *testing.T) {
	published := &container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{
		HostConfig: &container.HostConfig{PortBindings: nat.PortMap{"2376/tcp": {{HostIP: "127.0.0.1", HostPort: "40000"}}}},
	}}
	cases := []struct {
		name string
		d2k  bool
		args []string
		prev *container.InspectResponse
		want bool
	}{
		{"install with --d2k", true, []string{"--config=/etc/kubesolo/config.yaml"}, nil, true},
		{"upgrade of a flag-configured d2k container", false, []string{"--path=/var/lib/kubesolo", "--d2k", "--d2k-namespace=d2k"}, nil, true},
		{"upgrade of a d2k container with a config file", false, []string{"--config=/etc/kubesolo/config.yaml"}, published, true},
		{"no d2k", false, []string{"--path=/var/lib/kubesolo"}, nil, false},
		{"d2k namespace alone", false, []string{"--d2k-namespace=d2k"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := publishD2K(&config.Config{D2K: c.d2k}, c.args, c.prev); got != c.want {
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
	if env := proxyEnv("", []string{"172.20.0.0/16"}, clusterNetworks(nil)); env != nil {
		t.Errorf("no proxy: %v", env)
	}
	env := proxyEnv("http://proxy:3128", []string{"172.20.0.0/16"}, clusterNetworks(nil))
	want := []string{
		"HTTP_PROXY=http://proxy:3128",
		"HTTPS_PROXY=http://proxy:3128",
		"NO_PROXY=localhost,127.0.0.1,172.20.0.0/16,10.42.0.0/16,10.43.0.0/16,.svc,.cluster.local",
	}
	if strings.Join(env, " ") != strings.Join(want, " ") {
		t.Errorf("env = %v\nwant  %v", env, want)
	}
}

// The cluster networks excluded from the proxy are the container's own, as its
// configuration sets them.
func TestProxyEnvConfiguredNetworks(t *testing.T) {
	doc := []byte("apiVersion: kubesolo.io/v1alpha1\nkind: Config\nkubernetes:\n  podCIDR: 10.50.0.0/16\n  serviceCIDR: 10.51.0.0/16\n")
	env := proxyEnv("http://proxy:3128", nil, clusterNetworks(doc))
	if want := "NO_PROXY=localhost,127.0.0.1,10.50.0.0/16,10.51.0.0/16,.svc,.cluster.local"; env[2] != want {
		t.Errorf("NO_PROXY = %s\nwant       %s", env[2], want)
	}
	if got := strings.Join(clusterNetworks([]byte("not: [yaml")), ","); got != "10.42.0.0/16,10.43.0.0/16" {
		t.Errorf("an unreadable document: %s, want the defaults", got)
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

func TestParseVersionOutput(t *testing.T) {
	out := `{"level":"info","version":"v1.2.2","time":"2026-10-14T23:30:06Z","message":"kubesolo version"}` + "\n"
	if v, err := parseVersionOutput(out); err != nil || v != "v1.2.2" {
		t.Errorf("version = %q, %v", v, err)
	}
	if _, err := parseVersionOutput("kubesolo: error: unknown flag\n"); err == nil {
		t.Error("expected an error for output without a version")
	}
}

// The engine reports some pull failures inside the progress stream rather than
// as the request's error.
func TestLogPullProgressReturnsStreamError(t *testing.T) {
	ok := `{"status":"Pulling from portainer/kubesolo","id":"v1.2.2"}
{"status":"Digest: sha256:abc"}
`
	if err := logPullProgress(strings.NewReader(ok)); err != nil {
		t.Errorf("successful pull: %v", err)
	}
	failed := `{"status":"Pulling from portainer/kubesolo","id":"v9"}
{"errorDetail":{"message":"manifest unknown"},"error":"manifest unknown"}
`
	if err := logPullProgress(strings.NewReader(failed)); err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Errorf("failed pull: %v", err)
	}
}

func TestParseContainerMounts(t *testing.T) {
	got, err := ParseContainerMounts("/Users/me/src:/src, /tmp/registry:/var/lib/kubesolo/containerd/registry:ro")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/Users/me/src:/src", "/tmp/registry:/var/lib/kubesolo/containerd/registry:ro"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("binds = %v, want %v", got, want)
	}
	for _, bad := range []string{
		"relative:/src",
		"/src:relative",
		"/src",
		"/a:/b:rx",
		"/data:/var/lib/kubesolo",
		"/mods:/lib/modules",
		"/cfg:/etc/kubesolo",
	} {
		if _, err := ParseContainerMounts(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

// An upgrade or reset keeps the user's mounts and re-adds the ones kubesoloctl
// owns itself, so they are neither lost nor doubled.
func TestUserBinds(t *testing.T) {
	prev := []string{"kubesolo-dev-data:/var/lib/kubesolo", "kubesolo-dev-config:/etc/kubesolo", "/lib/modules:/lib/modules:ro", "/Users/me/src:/src"}
	if got := userBinds(prev); strings.Join(got, " ") != "/Users/me/src:/src" {
		t.Errorf("userBinds = %v", got)
	}
}

func TestBridgeMTU(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		want    int
		wantOK  bool
	}{
		{"set", map[string]string{dockerBridgeMTUOption: "1300"}, 1300, true},
		{"unset", map[string]string{}, 0, false},
		{"nil", nil, 0, false},
		{"not a number", map[string]string{dockerBridgeMTUOption: "auto"}, 0, false},
		{"zero", map[string]string{dockerBridgeMTUOption: "0"}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := bridgeMTU(tt.options)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("bridgeMTU(%v) = %d, %v; want %d, %v", tt.options, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
