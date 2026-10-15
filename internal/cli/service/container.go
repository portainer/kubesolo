package service

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/dockerhost"
	hostnetwork "github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// dockerBridgeMTUOption is the stable Docker bridge-driver network option key
// (not versioned by the Engine API).
const dockerBridgeMTUOption = "com.docker.network.driver.mtu"

// defaultDockerBridgeMTU is Docker's own bridge default, used when cmdArgs
// has no --mtu= (e.g. a CMD captured from a pre-MTU-support install).
const defaultDockerBridgeMTU = 1500

// networkNameFor returns the dedicated Docker network name for a given
// instance name, mirroring ContainerNameFor's per-instance scoping.
func networkNameFor(name string) string { return ContainerNameFor(name) + "-net" }

func (m *containerManager) nname() string { return networkNameFor(m.name) }

// mtuFromCmdArgs extracts --mtu=<value> from cmdArgs. Deriving the override
// case from cmdArgs — not cfg.MTU — keeps the outer Docker network in
// lockstep with whatever value is literally in the container's CMD across
// install/upgrade/reset, since upgrade and reset reuse a previously-captured
// CMD rather than a freshly resolved config.
//
// When cmdArgs has no --mtu= (the common case: the user didn't override),
// this is kubesoloctl auto-detecting on the host, so it can safely call the
// same interface-selection logic the embedded runtime uses rather than
// falling back to Docker's hardcoded 1500 default. cmdArgs itself is left
// unchanged — the container's CMD still has no --mtu=, so once it's attached
// to a network sized to this value, the embedded kubesolo process detects
// the same MTU from its own veth and still reports it as auto-detected, not
// pinned.
func mtuFromCmdArgs(cmdArgs []string) int {
	for _, a := range cmdArgs {
		v, ok := strings.CutPrefix(a, "--mtu=")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if mtu, err := hostnetwork.GetNodeMTU(); err == nil {
		return mtu
	}
	return defaultDockerBridgeMTU
}

// ensureNetwork makes sure a dedicated bridge network named nname exists with
// the given MTU: creates it if missing; if it exists with a different MTU,
// removes and recreates it. Callers must ensure no container is attached to
// nname first (Docker refuses to remove a network with attached endpoints).
func ensureNetwork(ctx context.Context, cli *client.Client, nname string, mtu int) error {
	insp, err := cli.NetworkInspect(ctx, nname, network.InspectOptions{})
	switch {
	case err == nil:
		current := defaultDockerBridgeMTU
		if v, ok := insp.Options[dockerBridgeMTUOption]; ok {
			if parsed, perr := strconv.Atoi(v); perr == nil {
				current = parsed
			}
		}
		if current == mtu {
			return nil
		}
		log.Info().Msgf("network %q MTU changed (%d -> %d); recreating network", nname, current, mtu)
		if err := cli.NetworkRemove(ctx, nname); err != nil {
			return fmt.Errorf("failed to remove network %q for MTU change: %w", nname, err)
		}
	case cerrdefs.IsNotFound(err):
		// fall through to create
	default:
		return fmt.Errorf("failed to inspect network %q: %w", nname, err)
	}

	if _, err := cli.NetworkCreate(ctx, nname, network.CreateOptions{
		Driver:  "bridge",
		Options: map[string]string{dockerBridgeMTUOption: strconv.Itoa(mtu)},
	}); err != nil {
		return fmt.Errorf("failed to create network %q: %w", nname, err)
	}
	log.Info().Msgf("network %q ready (mtu=%d)", nname, mtu)
	return nil
}

// ContainerNameFor returns the Docker container name for a given instance name.
// The default name ("kubesolo") is used as-is; any other name is prefixed with
// "kubesolo-" to namespace it away from unrelated Docker workloads.
func ContainerNameFor(name string) string {
	if name == "" || name == config.AppName {
		return config.AppName
	}
	return "kubesolo-" + name
}

type containerManager struct {
	name string
}

func (m *containerManager) cname() string { return ContainerNameFor(m.name) }

func (m *containerManager) Install(cfg *config.Config, cmdArgs []string) error {
	prev, err := InspectInstance(m.name)
	if err != nil && !cerrdefs.IsNotFound(err) {
		return err
	}
	return ReplaceContainer(m.name, cfg, cmdArgs, nil, prev)
}

// InspectInstance returns the named instance's container as the engine
// reports it.
func InspectInstance(name string) (*container.InspectResponse, error) {
	cli, err := newContainerClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	insp, err := cli.ContainerInspect(context.Background(), ContainerNameFor(name))
	if err != nil {
		return nil, err
	}
	return &insp, nil
}

// ReplaceContainer replaces the named instance's container, if it has one, with
// a new one running cfg's image with cmdArgs. A non-nil configDoc becomes its
// configuration file; nil keeps the one in its configuration volume. The
// hostname, host ports and proxy carry over from prev, the container being
// replaced as it was before anything changed; nil means there is none. An
// upgrade passes the snapshot it took before stopping the container, so a
// rollback restores them even when the new container could not be created.
func ReplaceContainer(name string, cfg *config.Config, cmdArgs []string, configDoc []byte, prev *container.InspectResponse) error {
	m := &containerManager{name: name}
	img := cfg.ContainerImage
	if img == "" {
		img = config.DefaultContainerImage + ":" + cfg.Version
	}

	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	ctx := context.Background()

	// Capture any workload ports the existing container published before we
	// remove it, so an upgrade (which re-runs Install) preserves them when the
	// caller did not pass --container-ports. 6443/2376 are excluded — those are
	// re-bound below on the host ports they had; see hostPortFor.
	prevWorkloadPorts := nat.PortMap{}

	// The hostname is the node name, and the kubelet's client certificate and
	// the Node object in the datastore are both issued for it. Docker defaults
	// the hostname to the container ID, so every replacement container came up
	// as a new node the old certificate is not authorised for, and never became
	// Ready. A replacement keeps its predecessor's hostname, whatever it was; a
	// first install uses the container name, which survives replacement.
	hostname := m.cname()
	if insp := prev; insp != nil {
		for p, b := range insp.HostConfig.PortBindings {
			if p == "6443/tcp" || p == "2376/tcp" {
				continue
			}
			prevWorkloadPorts[p] = b
		}
		if insp.Config != nil && insp.Config.Hostname != "" {
			hostname = insp.Config.Hostname
		}
	}

	// Pull before touching the running container: a pull that fails (a typo in
	// the tag, no network) must leave the cluster running, not removed.
	if err := pullImage(ctx, cli, img); err != nil {
		return err
	}

	// Stop and remove any existing container so install is idempotent.
	timeout := 10
	_ = cli.ContainerStop(ctx, m.cname(), container.StopOptions{Timeout: &timeout})
	_ = cli.ContainerRemove(ctx, m.cname(), container.RemoveOptions{Force: true})

	mtu := mtuFromCmdArgs(cmdArgs)
	if err := ensureNetwork(ctx, cli, m.nname(), mtu); err != nil {
		return err
	}
	var subnets []string
	if nw, err := cli.NetworkInspect(ctx, m.nname(), network.InspectOptions{}); err == nil {
		for _, c := range nw.IPAM.Config {
			subnets = append(subnets, c.Subnet)
		}
	}

	// Bind to 127.0.0.1 on a port of our choosing, so multiple named clusters
	// can run concurrently; see hostPortFor.
	apiPort, err := hostPortFor(prev, "6443/tcp")
	if err != nil {
		return err
	}
	portBindings := nat.PortMap{
		"6443/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: apiPort}},
	}
	exposedPorts := nat.PortSet{"6443/tcp": struct{}{}}
	if publishD2K(cfg, cmdArgs, prev) {
		d2kPort, err := hostPortFor(prev, "2376/tcp")
		if err != nil {
			return err
		}
		portBindings["2376/tcp"] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: d2kPort}}
		exposedPorts["2376/tcp"] = struct{}{}
	}

	// Host directory mounts, like workload ports: explicit --container-mounts if
	// given, otherwise whatever the previous container mounted.
	var mounts []string
	if cfg.ContainerMounts != "" {
		if mounts, err = ParseContainerMounts(cfg.ContainerMounts); err != nil {
			return err
		}
	} else if prev != nil && prev.HostConfig != nil {
		mounts = userBinds(prev.HostConfig.Binds)
	}

	// Publish workload ports: explicit --container-ports if given, otherwise
	// inherit whatever the previous container published (upgrade preservation).
	if cfg.ContainerPorts != "" {
		exposed, bindings, err := ParseContainerPorts(cfg.ContainerPorts)
		if err != nil {
			return err
		}
		for p, b := range bindings {
			portBindings[p] = b
		}
		for p := range exposed {
			exposedPorts[p] = struct{}{}
		}
	} else {
		for p, b := range prevWorkloadPorts {
			portBindings[p] = b
			exposedPorts[p] = struct{}{}
		}
	}

	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:        img,
			Hostname:     hostname,
			Cmd:          cmdArgs,
			Env:          proxyEnv(proxyFor(cfg, prev), subnets),
			ExposedPorts: exposedPorts,
		},
		// The cgroup namespace is left to the engine: private on cgroup v2, where
		// KubeSolo then nests itself and its pods under the container's own
		// cgroup, so the container's resource limits and accounting cover them.
		// The host's namespace would put them at the engine's top-level cgroup,
		// outside the container and shared with every other instance.
		&container.HostConfig{
			Privileged:    true,
			Binds:         containerBinds(name, mounts),
			PortBindings:  portBindings,
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			NetworkMode:   container.NetworkMode(m.nname()),
		},
		nil, nil, m.cname(),
	)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	if configDoc != nil {
		if err := writeContainerConfig(ctx, cli, resp.ID, configDoc); err != nil {
			return err
		}
	}
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	log.Info().Msgf("container %q started (id: %.12s)", m.cname(), resp.ID)
	return nil
}

// hostPortFor returns the host port to publish the container port p on. A
// port left for Docker to choose is chosen again on every start of the
// container, so a restart of the engine or the machine would leave the
// kubeconfig and the d2k context pointing at a dead port. Like kind, the port is
// therefore chosen here and pinned: the one the previous container is bound to,
// so its replacement keeps it, or else a free one.
func hostPortFor(prev *container.InspectResponse, p nat.Port) (string, error) {
	if prev != nil {
		if prev.NetworkSettings != nil {
			if b := prev.NetworkSettings.Ports[p]; len(b) > 0 && b[0].HostPort != "" {
				return b[0].HostPort, nil
			}
		}
		if prev.HostConfig != nil {
			if b := prev.HostConfig.PortBindings[p]; len(b) > 0 && b[0].HostPort != "" {
				return b[0].HostPort, nil
			}
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("failed to find a free port for %s: %w", p, err)
	}
	defer func() { _ = l.Close() }()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port), nil
}

// proxyFor returns the proxy the container uses: --proxy when given, and
// otherwise the previous container's, since an upgrade does not take --proxy.
func proxyFor(cfg *config.Config, prev *container.InspectResponse) string {
	if cfg.Proxy != "" || prev == nil || prev.Config == nil {
		return cfg.Proxy
	}
	for _, e := range prev.Config.Env {
		if v, ok := strings.CutPrefix(e, "HTTP_PROXY="); ok {
			return v
		}
	}
	return ""
}

// proxyEnv returns the proxy environment of the KubeSolo container, as the
// service definition of a host install sets it. Unlike there, the node's own
// addresses are not localhost: KubeSolo reaches its API server on the address
// it has on the instance network, and pods and services on their own ranges,
// none of which a proxy outside the container can reach. As kind does, these
// are excluded alongside the cluster's DNS suffixes.
func proxyEnv(proxy string, subnets []string) []string {
	if proxy == "" {
		return nil
	}
	noProxy := append([]string{"localhost", "127.0.0.1"}, subnets...)
	noProxy = append(noProxy, types.DefaultPodCIDR, types.DefaultServiceClusterIPRange, ".svc", ".cluster.local")
	return []string{
		"HTTP_PROXY=" + proxy,
		"HTTPS_PROXY=" + proxy,
		"NO_PROXY=" + strings.Join(noProxy, ","),
	}
}

// containerBinds returns the mounts of the named instance's container: its data
// volume, its configuration volume, the kernel modules of the engine's host,
// read-only, as kind mounts them, so that KubeSolo can load the netfilter
// modules it needs on a host that has not loaded them yet, and the user's own
// mounts.
func containerBinds(name string, mounts []string) []string {
	return append([]string{
		ContainerNameFor(name) + "-data:" + containerDataDir,
		ContainerNameFor(name) + "-config:" + containerConfigDir,
		"/lib/modules:/lib/modules:ro",
	}, mounts...)
}

// ReadContainerConfig returns the configuration file of the named instance's
// container, running or not.
func ReadContainerConfig(name string) ([]byte, error) {
	cli, err := newContainerClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	rc, _, err := cli.CopyFromContainer(context.Background(), ContainerNameFor(name), config.ContainerConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s from %s: %w", config.ContainerConfigFile, ContainerNameFor(name), err)
	}
	defer func() { _ = rc.Close() }()

	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", config.ContainerConfigFile, err)
	}
	return io.ReadAll(tr)
}

// WriteContainerConfig replaces the configuration file of the named instance's
// container, running or not. KubeSolo reads it when it next starts.
func WriteContainerConfig(name string, doc []byte) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()
	return writeContainerConfig(context.Background(), cli, ContainerNameFor(name), doc)
}

// containerConfigDir holds the configuration file of a KubeSolo container.
var containerConfigDir = path.Dir(config.ContainerConfigFile)

// writeContainerConfig puts the configuration file doc into the configuration
// volume of the created, not yet started, container id.
func writeContainerConfig(ctx context.Context, cli *client.Client, id string, doc []byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:    path.Base(config.ContainerConfigFile),
		Mode:    0o600,
		Size:    int64(len(doc)),
		ModTime: time.Now(),
	}); err != nil {
		return err
	}
	if _, err := tw.Write(doc); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := cli.CopyToContainer(ctx, id, containerConfigDir, &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("failed to write %s: %w", config.ContainerConfigFile, err)
	}
	return nil
}

// publishD2K reports whether the container needs the d2k port published. An
// upgrade passes the previous container's command line with a config that does
// not set D2K: a container configured by flags names --d2k there, and one with
// a configuration file published the port.
func publishD2K(cfg *config.Config, cmdArgs []string, prev *container.InspectResponse) bool {
	if cfg.D2K || slices.Contains(cmdArgs, "--d2k") {
		return true
	}
	return prev != nil && prev.HostConfig != nil && len(prev.HostConfig.PortBindings["2376/tcp"]) > 0
}

func (m *containerManager) Uninstall() error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	ctx := context.Background()
	timeout := 30
	log.Info().Msgf("stopping container %q...", m.cname())
	_ = cli.ContainerStop(ctx, m.cname(), container.StopOptions{Timeout: &timeout})
	log.Info().Msgf("removing container %q...", m.cname())
	if err := cli.ContainerRemove(ctx, m.cname(), container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("failed to remove container %q: %w", m.cname(), err)
	}

	nname := m.nname()
	log.Info().Msgf("removing network %q...", nname)
	if err := cli.NetworkRemove(ctx, nname); err != nil && !cerrdefs.IsNotFound(err) {
		log.Warn().Msgf("could not remove network %q: %v", nname, err)
	}
	return nil
}

// ContainerVersion returns the version of KubeSolo in the named instance's
// running container, as the binary reports it.
func ContainerVersion(name string) (string, error) {
	cli, err := newContainerClient()
	if err != nil {
		return "", err
	}
	defer func() { _ = cli.Close() }()

	ctx := context.Background()
	exec, err := cli.ContainerExecCreate(ctx, ContainerNameFor(name), container.ExecOptions{
		Cmd:          []string{"/usr/local/bin/kubesolo", "--version"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", err
	}
	resp, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, resp.Reader); err != nil {
		return "", err
	}
	return parseVersionOutput(out.String())
}

// parseVersionOutput finds the version in what kubesolo --version prints: a
// JSON log line with a version field.
func parseVersionOutput(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		var entry struct {
			Version string `json:"version"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Version != "" {
			return entry.Version, nil
		}
	}
	return "", fmt.Errorf("no version in %q", strings.TrimSpace(out))
}

// RemoveContainerVolume removes the data volume for the named instance.
// Called during uninstall --purge to delete all cluster state.
func RemoveContainerVolume(name string) error {
	_, err := removeVolume(ContainerNameFor(name) + "-data")
	return err
}

// RemoveContainerConfigVolume removes the configuration volume of the named
// instance, reporting whether there was one.
func RemoveContainerConfigVolume(name string) (bool, error) {
	return removeVolume(ContainerNameFor(name) + "-config")
}

func removeVolume(vname string) (bool, error) {
	cli, err := newContainerClient()
	if err != nil {
		return false, err
	}
	defer func() { _ = cli.Close() }()

	if err := cli.VolumeRemove(context.Background(), vname, true); err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to remove volume %s: %w", vname, err)
	}
	log.Info().Msgf("volume %s removed", vname)
	return true, nil
}

// ResetContainer wipes cluster state and starts fresh: it captures the existing
// container's image and CMD, removes the container and data volume, then
// re-creates the container with the same configuration.
func ResetContainer(name string) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	ctx := context.Background()
	cname := ContainerNameFor(name)
	vname := cname + "-data"

	// Capture existing container config before removing it.
	resp, err := cli.ContainerInspect(ctx, cname)
	if err != nil {
		return fmt.Errorf("container %q not found — is KubeSolo installed? (%w)", cname, err)
	}
	img := resp.Config.Image
	cmdArgs := resp.Config.Cmd
	portBindings := resp.HostConfig.PortBindings
	exposedPorts := resp.Config.ExposedPorts
	for _, p := range []nat.Port{"6443/tcp", "2376/tcp"} {
		if b := portBindings[p]; len(b) > 0 {
			hp, err := hostPortFor(&resp, p)
			if err != nil {
				return err
			}
			portBindings[p] = []nat.PortBinding{{HostIP: b[0].HostIP, HostPort: hp}}
		}
	}

	// Stop and remove the container.
	timeout := 30
	log.Info().Msgf("stopping container %q...", cname)
	_ = cli.ContainerStop(ctx, cname, container.StopOptions{Timeout: &timeout})
	log.Info().Msgf("removing container %q...", cname)
	_ = cli.ContainerRemove(ctx, cname, container.RemoveOptions{Force: true})

	nname := networkNameFor(name)
	mtu := mtuFromCmdArgs(cmdArgs)
	if err := ensureNetwork(ctx, cli, nname, mtu); err != nil {
		return err
	}

	// Remove the data volume to clear all cluster state.
	log.Info().Msgf("removing volume %s...", vname)
	_ = cli.VolumeRemove(ctx, vname, true)

	// Re-create the container with the same image, CMD, and port bindings.
	log.Info().Msgf("creating fresh container %q...", cname)
	createResp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: img,
			// The volume is fresh, so the node identity can move to the stable
			// container name; see Install.
			Hostname:     cname,
			Cmd:          cmdArgs,
			Env:          resp.Config.Env,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Privileged:    true,
			Binds:         containerBinds(name, userBinds(resp.HostConfig.Binds)),
			PortBindings:  portBindings,
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			NetworkMode:   container.NetworkMode(nname),
		},
		nil, nil, cname,
	)
	if err != nil {
		return fmt.Errorf("failed to recreate container: %w", err)
	}

	if err := cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start fresh container: %w", err)
	}

	log.Info().Msgf("container %q started fresh (id: %.12s)", cname, createResp.ID)
	return nil
}

// GetContainerAPIPort returns the host port Docker mapped to the container's
// API server (6443/tcp). Call this after Install to discover the port.
func GetContainerAPIPort(name string) (int, error) {
	cli, err := newContainerClient()
	if err != nil {
		return 0, err
	}
	defer func() { _ = cli.Close() }()

	cname := ContainerNameFor(name)
	resp, err := cli.ContainerInspect(context.Background(), cname)
	if err != nil {
		return 0, fmt.Errorf("container %q not found: %w", cname, err)
	}

	bindings := resp.NetworkSettings.Ports["6443/tcp"]
	if len(bindings) == 0 {
		return 0, fmt.Errorf("container %q has no host binding for 6443/tcp", cname)
	}

	port, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return 0, fmt.Errorf("invalid host port %q: %w", bindings[0].HostPort, err)
	}
	return port, nil
}

// GetContainerD2KPort returns the host port Docker mapped to the container's
// D2K endpoint (2376/tcp). Returns an error if D2K was not enabled on this
// instance (port not published), guiding the user to reinstall with --d2k.
func GetContainerD2KPort(name string) (int, error) {
	cli, err := newContainerClient()
	if err != nil {
		return 0, err
	}
	defer func() { _ = cli.Close() }()

	cname := ContainerNameFor(name)
	resp, err := cli.ContainerInspect(context.Background(), cname)
	if err != nil {
		return 0, fmt.Errorf("container %q not found: %w", cname, err)
	}

	bindings := resp.NetworkSettings.Ports["2376/tcp"]
	if len(bindings) == 0 {
		return 0, fmt.Errorf("container %q has no host binding for 2376/tcp — was it installed with --d2k?", cname)
	}

	port, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return 0, fmt.Errorf("invalid host port %q: %w", bindings[0].HostPort, err)
	}
	return port, nil
}

// ContainerLogTail returns the last lines KubeSolo logged in the named
// instance's container, for reporting why it failed to come up.
func ContainerLogTail(name string, lines int) ([]string, error) {
	cli, err := newContainerClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	rc, err := cli.ContainerLogs(context.Background(), ContainerNameFor(name), container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(lines),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, rc); err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(out.String(), "\n"), "\n"), nil
}

// ContainerExistsByName reports whether a container with the given name exists
// (running or stopped). It returns false — never an error — when the container
// engine is unreachable or no such container exists, so callers can use it to
// detect container run mode on any host without a hard dependency on a reachable
// engine (a host-service install on a box with no engine simply reports false).
func ContainerExistsByName(cname string) bool {
	cli, err := newContainerClient()
	if err != nil {
		return false
	}
	defer func() { _ = cli.Close() }()
	_, err = cli.ContainerInspect(context.Background(), cname)
	return err == nil
}

// ContainerExists reports whether the KubeSolo container for the given instance
// name exists. Convenience wrapper around ContainerExistsByName.
func ContainerExists(name string) bool {
	return ContainerExistsByName(ContainerNameFor(name))
}

// InstanceExists reports whether anything of the named container instance is
// left: its container, network, data volume or configuration volume. A container removed by hand
// leaves the other two behind, and uninstall must still find them.
func InstanceExists(name string) bool {
	cli, err := newContainerClient()
	if err != nil {
		return false
	}
	defer func() { _ = cli.Close() }()

	ctx := context.Background()
	if _, err := cli.ContainerInspect(ctx, ContainerNameFor(name)); err == nil {
		return true
	}
	if _, err := cli.NetworkInspect(ctx, networkNameFor(name), network.InspectOptions{}); err == nil {
		return true
	}
	for _, v := range []string{"-data", "-config"} {
		if _, err := cli.VolumeInspect(ctx, ContainerNameFor(name)+v); err == nil {
			return true
		}
	}
	return false
}

// RemoveContainerNetwork removes the named instance's network, reporting
// whether there was one to remove.
func RemoveContainerNetwork(name string) (bool, error) {
	cli, err := newContainerClient()
	if err != nil {
		return false, err
	}
	defer func() { _ = cli.Close() }()

	if err := cli.NetworkRemove(context.Background(), networkNameFor(name)); err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to remove network %q: %w", networkNameFor(name), err)
	}
	return true, nil
}

func newContainerClient() (*client.Client, error) {
	cli, err := dockerhost.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}
	return cli, nil
}

// PullImage pulls img.
func PullImage(img string) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()
	return pullImage(context.Background(), cli, img)
}

// pullImage pulls img. When the pull fails but the engine has img already —
// an image built or loaded locally, or a machine without network — the local
// copy is used, as docker run does.
// pulled records the images this run has pulled already: an upgrade pulls
// before it stops anything and must not pull, or warn, a second time when the
// container is created.
var pulled = map[string]bool{}

func pullImage(ctx context.Context, cli *client.Client, img string) error {
	if pulled[img] {
		return nil
	}
	defer func() { pulled[img] = true }()
	log.Info().Msgf("pulling %s...", img)
	rc, err := cli.ImagePull(ctx, img, image.PullOptions{})
	if err == nil {
		err = logPullProgress(rc)
		_ = rc.Close()
	}
	if err == nil {
		return nil
	}
	if _, inspectErr := cli.ImageInspect(ctx, img); inspectErr == nil {
		log.Warn().Msgf("could not pull %s (%v); using the local image", img, err)
		return nil
	}
	return fmt.Errorf("failed to pull image %s: %w", img, err)
}

// logPullProgress reads the Docker image pull JSON stream and emits zerolog
// INFO lines for meaningful status changes (skips per-byte progress updates).
// It returns the error the stream reports, if any: the engine reports some pull
// failures there rather than as the request's error.
func logPullProgress(r io.Reader) error {
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		if msg, ok := m["error"].(string); ok && msg != "" {
			return errors.New(msg)
		}
		status, _ := m["status"].(string)
		id, _ := m["id"].(string)
		_, hasProgress := m["progressDetail"]
		if hasProgress || status == "" {
			continue
		}
		key := status + "|" + id
		if !seen[key] {
			seen[key] = true
			if id != "" {
				log.Info().Msgf("%s: %s", status, id)
			} else {
				log.Info().Msgf("%s", status)
			}
		}
	}
	return scanner.Err()
}

// LoadImages copies images from the engine into the named instance's KubeSolo,
// as kind load docker-image does for a kind node: the engine saves them as an
// archive, which KubeSolo imports inside the container. It returns the names
// the images were imported under.
func LoadImages(name string, images []string) ([]string, error) {
	cli, err := newContainerClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	ctx := context.Background()
	archive, err := cli.ImageSave(ctx, images)
	if err != nil {
		return nil, fmt.Errorf("failed to export %s from the engine: %w", strings.Join(images, ", "), err)
	}
	defer func() { _ = archive.Close() }()

	exec, err := cli.ContainerExecCreate(ctx, ContainerNameFor(name), container.ExecOptions{
		Cmd:          []string{"/usr/local/bin/kubesolo", "--import-images=" + config.DefaultPath},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}
	resp, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(resp.Conn, archive)
		_ = resp.CloseWrite()
		copied <- err
	}()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return nil, err
	}
	if err := <-copied; err != nil {
		return nil, fmt.Errorf("failed to send the images to the container: %w", err)
	}

	result, err := cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "import-images") {
			return nil, fmt.Errorf("the KubeSolo in %s cannot import images; upgrade it to a release with kubesoloctl load support", ContainerNameFor(name))
		}
		return nil, fmt.Errorf("importing the images failed: %s", msg)
	}
	return strings.Fields(stdout.String()), nil
}
