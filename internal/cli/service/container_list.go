package service

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/portainer/kubesolo/internal/cli/config"
)

// Instance is a KubeSolo container instance as the engine reports it.
type Instance struct {
	Name      string `json:"name"`
	Container string `json:"container"`
	Image     string `json:"image"`
	State     string `json:"state"`
	APIServer string `json:"apiServer,omitempty"`
}

// ListInstances returns the KubeSolo container instances on the engine: the
// containers named as ContainerNameFor names them that hold a KubeSolo data
// volume.
func ListInstances() ([]Instance, error) {
	cli, err := newContainerClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	containers, err := cli.ContainerList(context.Background(), container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", config.AppName)),
	})
	if client.IsErrConnectionFailed(err) {
		return nil, fmt.Errorf("no container engine is reachable, so there are no container instances to list (%w)", err)
	}
	if err != nil {
		return nil, err
	}

	var out []Instance
	for _, c := range containers {
		if len(c.Names) == 0 {
			continue
		}
		cname := strings.TrimPrefix(c.Names[0], "/")
		name, ok := instanceName(cname)
		if !ok || !mountsData(c.Mounts) {
			continue
		}
		inst := Instance{Name: name, Container: cname, Image: c.Image, State: c.State}
		for _, p := range c.Ports {
			if p.PrivatePort == 6443 && p.PublicPort != 0 {
				inst.APIServer = "https://127.0.0.1:" + strconv.Itoa(int(p.PublicPort))
			}
		}
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// instanceName is the inverse of ContainerNameFor.
func instanceName(cname string) (string, bool) {
	if cname == config.AppName {
		return config.AppName, true
	}
	name, ok := strings.CutPrefix(cname, config.AppName+"-")
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

func mountsData(mounts []container.MountPoint) bool {
	for _, m := range mounts {
		if m.Destination == containerDataDir {
			return true
		}
	}
	return false
}

// StreamLogs writes what KubeSolo logged in the named instance's container to
// w: the last tail lines ("all" for everything), and, with follow, everything
// it logs afterwards until it stops.
func StreamLogs(name string, follow bool, tail string, w io.Writer) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	rc, err := cli.ContainerLogs(context.Background(), ContainerNameFor(name), container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: follow, Tail: tail,
	})
	if err != nil {
		return fmt.Errorf("container %q: %w", ContainerNameFor(name), err)
	}
	defer func() { _ = rc.Close() }()
	_, err = stdcopy.StdCopy(w, w, rc)
	return err
}
