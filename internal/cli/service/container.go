package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

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
	defer cli.Close()

	ctx := context.Background()

	// Stop and remove any existing container so install is idempotent.
	timeout := 10
	_ = cli.ContainerStop(ctx, m.cname(), container.StopOptions{Timeout: &timeout})
	_ = cli.ContainerRemove(ctx, m.cname(), container.RemoveOptions{Force: true})

	// Pull image, streaming meaningful status lines to zerolog.
	log.Info().Msgf("pulling %s...", img)
	rc, err := cli.ImagePull(ctx, img, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", img, err)
	}
	logPullProgress(rc)
	rc.Close()

	// Bind to 127.0.0.1 with an empty HostPort so Docker picks a random
	// ephemeral port. This allows multiple named clusters to run concurrently.
	portBindings := nat.PortMap{
		"6443/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
	}
	if cfg.D2K {
		portBindings["2376/tcp"] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}
	}

	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: img,
			Cmd:   cmdArgs,
		},
		&container.HostConfig{
			Privileged:    true,
			CgroupnsMode:  container.CgroupnsModeHost,
			Binds:         []string{m.vname() + ":/var/lib/kubesolo"},
			PortBindings:  portBindings,
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
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
	defer cli.Close()

	ctx := context.Background()
	timeout := 30
	log.Info().Msgf("stopping container %q...", m.cname())
	_ = cli.ContainerStop(ctx, m.cname(), container.StopOptions{Timeout: &timeout})
	log.Info().Msgf("removing container %q...", m.cname())
	if err := cli.ContainerRemove(ctx, m.cname(), container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("failed to remove container %q: %w", m.cname(), err)
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
	defer cli.Close()

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
	defer cli.Close()

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
	defer cli.Close()

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

	// Stop and remove the container.
	timeout := 30
	log.Info().Msgf("stopping container %q...", cname)
	_ = cli.ContainerStop(ctx, cname, container.StopOptions{Timeout: &timeout})
	log.Info().Msgf("removing container %q...", cname)
	_ = cli.ContainerRemove(ctx, cname, container.RemoveOptions{Force: true})

	// Remove the data volume to clear all cluster state.
	log.Info().Msgf("removing volume %s...", vname)
	_ = cli.VolumeRemove(ctx, vname, true)

	// Re-create the container with the same image, CMD, and port bindings.
	log.Info().Msgf("creating fresh container %q...", cname)
	createResp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: img,
			Cmd:   cmdArgs,
		},
		&container.HostConfig{
			Privileged:    true,
			CgroupnsMode:  container.CgroupnsModeHost,
			Binds:         []string{vname + ":/var/lib/kubesolo"},
			PortBindings:  portBindings,
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
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
	defer cli.Close()

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
	defer cli.Close()

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
