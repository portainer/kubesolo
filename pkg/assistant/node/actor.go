package node

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/internal/upgrade/executor"
	"github.com/portainer/kubesolo/pkg/assistant"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	internalapi "k8s.io/cri-api/pkg/apis"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	criclient "k8s.io/cri-client/pkg"
)

// Actor carries out the assistant's actions on the node. Each is checked
// against the node as it is before it is carried out.
type Actor struct {
	// Checks are the assistant's checks: each component's health probe, and
	// the host's.
	Checks map[string]func(context.Context) error

	// Layout, Version and Spawn queue a restart with the upgrade executor,
	// which stops and starts KubeSolo from outside it.
	Layout  upgrade.Layout
	Version string
	Spawn   func(jobFile string) error

	// RuntimeEndpoint is the CRI runtime's, for images and pods, and
	// CgroupRoot where pod cgroups are, for orphans.
	RuntimeEndpoint string
	CgroupRoot      string
}

// Check says why the action must not be carried out now, or returns nil.
func (a *Actor) Check(ctx context.Context, action string, _ assistant.Incident) error {
	switch action {
	case "restart_component":
		if failing := a.failingComponents(ctx); len(failing) == 0 {
			return errors.New("every component is healthy now")
		}
		return nil
	case "repair_database":
		if err := executor.QuickCheck(a.Layout.Datastore()); err == nil {
			return errors.New("the datastore passes its integrity check now; a repair would only drop its latest writes")
		}
		return nil
	case "prune_images":
		if disk, ok := a.Checks[assistant.CheckDisk]; !ok || disk(ctx) == nil {
			return errors.New("the disk is not full now")
		}
		_, unused, err := a.pruneable(ctx)
		if err != nil {
			return fmt.Errorf("list the images: %w", err)
		}
		if len(unused) == 0 {
			return errors.New("no image is unused: every one is in use by a container")
		}
		return nil
	case "clean_orphaned_containers":
		orphans, err := a.orphans(ctx)
		if err != nil {
			return fmt.Errorf("look for orphaned containers: %w", err)
		}
		if len(orphans) == 0 {
			return errors.New("no container's processes outlive it now")
		}
		return nil
	}
	return fmt.Errorf("KubeSolo does not carry out %s yet; it is reported only", action)
}

// Run carries the action out. A restart is left to the upgrade executor,
// whose job ID it returns.
func (a *Actor) Run(ctx context.Context, action string, inc assistant.Incident) (string, error) {
	switch action {
	case "restart_component", "repair_database":
		job := upgrade.Job{
			ID: upgrade.NewRunID(), Operation: upgrade.OpRestart,
			Request: upgrade.Request{Version: a.Version, RepairDatastore: action == "repair_database"},
			DataDir: a.Layout.DataDir, ConfigFile: a.Layout.ConfigFile, From: a.Version,
		}
		accepted, err := upgrade.Queue(a.Layout, job, a.Spawn, "asked by the assistant, for incident "+inc.ID)
		if err != nil {
			return "", err
		}
		return accepted.ID, nil
	case "prune_images":
		images, unused, err := a.pruneable(ctx)
		if err != nil {
			return "", err
		}
		var errs []error
		for _, img := range unused {
			if err := images.RemoveImage(ctx, &runtimeapi.ImageSpec{Image: img.Id}); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", strings.Join(img.RepoTags, ", "), err))
			}
		}
		log.Info().Str("component", "assistant").Int("images", len(unused)-len(errs)).Msg("pruned unused images")
		return "", errors.Join(errs...)
	case "clean_orphaned_containers":
		orphans, err := a.orphans(ctx)
		if err != nil {
			return "", err
		}
		var errs []error
		for _, o := range orphans {
			if err := killCgroup(o.dir); err != nil {
				errs = append(errs, fmt.Errorf("container %s: %w", o.id[:12], err))
			}
		}
		log.Info().Str("component", "assistant").Int("containers", len(orphans)-len(errs)).Msg("killed the processes of orphaned containers")
		return "", errors.Join(errs...)
	}
	return "", fmt.Errorf("KubeSolo does not carry out %s yet", action)
}

// restartSettleTime is how long after a restart its components may take to
// come up before one still failing makes it a failure.
const restartSettleTime = 5 * time.Minute

// Settle reports whether the executor's job has ended, from its record, and
// whether it fixed the fault: KubeSolo back healthy and no component failing.
func (a *Actor) Settle(ctx context.Context, job string) (bool, error) {
	state, err := upgrade.LoadState(a.Layout)
	if err != nil {
		return false, nil // try again later
	}
	switch {
	case state.Current != nil && state.Current.ID == job:
		return false, nil
	case state.Last != nil && state.Last.ID == job:
		if state.Last.Result != upgrade.ResultSucceeded {
			return true, fmt.Errorf("the restart %s: %s", state.Last.Result, state.Last.Error)
		}
		failing := a.failingComponents(ctx)
		if len(failing) == 0 {
			return true, nil
		}
		if time.Since(state.Last.Finished) < restartSettleTime {
			return false, nil
		}
		return true, fmt.Errorf("KubeSolo restarted, but %s", strings.Join(failing, "; "))
	}
	return true, errors.New("the restart's record is gone")
}

// failingComponents lists the control plane components failing now; the
// host's checks are not components, and a restart fixes neither.
func (a *Actor) failingComponents(ctx context.Context) []string {
	var failing []string
	for _, name := range slices.Sorted(maps.Keys(a.Checks)) {
		if name == assistant.CheckDisk || name == assistant.CheckMemory {
			continue
		}
		if err := a.Checks[name](ctx); err != nil {
			failing = append(failing, name+": "+strings.SplitN(err.Error(), "\n", 2)[0])
		}
	}
	return failing
}

// orphanRecheck is how long apart two scans must both find a container
// orphaned: a container being started has its cgroup a moment before the
// runtime lists it.
const orphanRecheck = 10 * time.Second

// criClients connects to the CRI runtime's image and runtime services.
func (a *Actor) criClients() (internalapi.ImageManagerService, internalapi.RuntimeService, error) {
	images, err := criclient.NewRemoteImageService(a.RuntimeEndpoint, types.DefaultContextTimeout, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := criclient.NewRemoteRuntimeService(a.RuntimeEndpoint, types.DefaultContextTimeout, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	return images, runtime, nil
}

// pruneable lists the images to prune now.
func (a *Actor) pruneable(ctx context.Context) (internalapi.ImageManagerService, []*runtimeapi.Image, error) {
	images, runtime, err := a.criClients()
	if err != nil {
		return nil, nil, err
	}
	all, err := images.ListImages(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	containers, err := runtime.ListContainers(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	return images, unusedImages(all, containers), nil
}

// orphans lists the containers orphaned in two scans orphanRecheck apart.
func (a *Actor) orphans(ctx context.Context) ([]orphan, error) {
	scan := func() ([]orphan, error) {
		_, runtime, err := a.criClients()
		if err != nil {
			return nil, err
		}
		sandboxes, err := runtime.ListPodSandbox(ctx, &runtimeapi.PodSandboxFilter{
			State: &runtimeapi.PodSandboxStateValue{State: runtimeapi.PodSandboxState_SANDBOX_READY},
		})
		if err != nil {
			return nil, err
		}
		containers, err := runtime.ListContainers(ctx, nil)
		if err != nil {
			return nil, err
		}
		var live []string
		for _, sb := range sandboxes {
			live = append(live, sb.Id)
		}
		for _, c := range containers {
			if c.State != runtimeapi.ContainerState_CONTAINER_EXITED {
				live = append(live, c.Id)
			}
		}
		return orphanedContainers(a.CgroupRoot, live)
	}
	first, err := scan()
	if err != nil || len(first) == 0 {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(orphanRecheck):
	}
	second, err := scan()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(second, func(o orphan) bool {
		return !slices.ContainsFunc(first, func(f orphan) bool { return f.id == o.id })
	}), nil
}
