package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/portainer/kubesolo/internal/cli/config"
	hostnetwork "github.com/portainer/kubesolo/internal/runtime/network"
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
func (m *containerManager) vname() string { return ContainerNameFor(m.name) + "-data" }

func (m *containerManager) Install(cfg *config.Config, cmdArgs []string) error {
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
	// re-bound fresh below with new random host ports.
	prevWorkloadPorts := nat.PortMap{}
	if insp, err := cli.ContainerInspect(ctx, m.cname()); err == nil {
		for p, b := range insp.HostConfig.PortBindings {
			if p == "6443/tcp" || p == "2376/tcp" {
				continue
			}
			prevWorkloadPorts[p] = b
		}
	}

	// Stop and remove any existing container so install is idempotent.
	timeout := 10
	_ = cli.ContainerStop(ctx, m.cname(), container.StopOptions{Timeout: &timeout})
	_ = cli.ContainerRemove(ctx, m.cname(), container.RemoveOptions{Force: true})

	mtu := mtuFromCmdArgs(cmdArgs)
	if err := ensureNetwork(ctx, cli, m.nname(), mtu); err != nil {
		return err
	}

	// Pull image, streaming meaningful status lines to zerolog.
	log.Info().Msgf("pulling %s...", img)
	rc, err := cli.ImagePull(ctx, img, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", img, err)
	}
	logPullProgress(rc)
	_ = rc.Close()

	// Bind to 127.0.0.1 with an empty HostPort so Docker picks a random
	// ephemeral port. This allows multiple named clusters to run concurrently.
	portBindings := nat.PortMap{
		"6443/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
	}
	exposedPorts := nat.PortSet{"6443/tcp": struct{}{}}
	if cfg.D2K {
		portBindings["2376/tcp"] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}
		exposedPorts["2376/tcp"] = struct{}{}
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
			Cmd:          cmdArgs,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Privileged:    true,
			CgroupnsMode:  container.CgroupnsModeHost,
			Binds:         []string{m.vname() + ":/var/lib/kubesolo"},
			PortBindings:  portBindings,
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			NetworkMode:   container.NetworkMode(m.nname()),
		},
		nil, nil, m.cname(),
	)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	log.Info().Msgf("container %q started (id: %.12s)", m.cname(), resp.ID)
	return nil
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

// ContainerArgs returns the CMD of the running container identified by name.
// Used by upgrade to preserve the original configuration flags.
func ContainerArgs(name string) ([]string, error) {
	cli, err := newContainerClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	cname := ContainerNameFor(name)
	resp, err := cli.ContainerInspect(context.Background(), cname)
	if err != nil {
		return nil, fmt.Errorf("container %q not found — is KubeSolo installed? (%w)", cname, err)
	}
	return resp.Config.Cmd, nil
}

// RemoveContainerVolume removes the data volume for the named instance.
// Called during uninstall --purge to delete all cluster state.
func RemoveContainerVolume(name string) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	vname := ContainerNameFor(name) + "-data"
	if err := cli.VolumeRemove(context.Background(), vname, true); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("failed to remove volume %s: %w", vname, err)
	}
	log.Info().Msgf("volume %s removed", vname)
	return nil
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
			Image:        img,
			Cmd:          cmdArgs,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Privileged:    true,
			CgroupnsMode:  container.CgroupnsModeHost,
			Binds:         []string{vname + ":/var/lib/kubesolo"},
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
// API server (6443/tcp). Call this after Install to discover the random port.
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

func newContainerClient() (*client.Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}
	return cli, nil
}

// logPullProgress reads the Docker image pull JSON stream and emits zerolog
// INFO lines for meaningful status changes (skips per-byte progress updates).
func logPullProgress(r io.Reader) {
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
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
}
