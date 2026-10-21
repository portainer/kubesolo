package node

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// unusedImages are the images no container uses, running or stopped, and
// that are not pinned: the pause image is.
func unusedImages(images []*runtimeapi.Image, containers []*runtimeapi.Container) []*runtimeapi.Image {
	used := map[string]bool{}
	for _, c := range containers {
		used[c.ImageRef] = true
		if c.Image != nil {
			used[c.Image.Image] = true
		}
	}
	var out []*runtimeapi.Image
	for _, img := range images {
		if img.Pinned || used[img.Id] || slices.ContainsFunc(img.RepoTags, func(t string) bool { return used[t] }) {
			continue
		}
		out = append(out, img)
	}
	return out
}

// containerCgroup matches a container's cgroup directory:
// cri-containerd-<id>.scope under the systemd driver, or <id> under cgroupfs.
var containerCgroup = regexp.MustCompile(`^(?:cri-containerd-([0-9a-f]{64})\.scope|([0-9a-f]{64}))$`)

// orphan is a container cgroup whose container the runtime does not run.
type orphan struct {
	id  string
	dir string
}

// orphanedContainers lists the container cgroups under cgroupRoot that still
// hold processes but whose ID is not among live, the IDs of the runtime's
// live containers and sandboxes.
func orphanedContainers(cgroupRoot string, live []string) ([]orphan, error) {
	var out []orphan
	for _, top := range []string{"kubepods.slice", "kubepods"} {
		base := filepath.Join(cgroupRoot, top)
		if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			m := containerCgroup.FindStringSubmatch(d.Name())
			if m == nil {
				return nil
			}
			id := m[1] + m[2]
			if !slices.Contains(live, id) && len(cgroupProcs(path)) > 0 {
				out = append(out, orphan{id: id, dir: path})
			}
			return fs.SkipDir
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// cgroupProcs lists the processes in the cgroup and the cgroups below it.
func cgroupProcs(dir string) []int {
	var pids []int
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.Name() != "cgroup.procs" {
			return nil
		}
		raw, _ := os.ReadFile(path)
		for _, f := range strings.Fields(string(raw)) {
			if pid, err := strconv.Atoi(f); err == nil {
				pids = append(pids, pid)
			}
		}
		return nil
	})
	return pids
}

// killCgroup kills every process in the cgroup: at once through cgroup.kill
// where the kernel has it, otherwise one process at a time.
func killCgroup(dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0o644); err == nil {
		return nil
	}
	var errs []error
	for _, pid := range cgroupProcs(dir) {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
