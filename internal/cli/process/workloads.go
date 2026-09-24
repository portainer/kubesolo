package process

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// StopWorkloads terminates every container process and every shim left behind
// once the KubeSolo service has stopped.
//
// Pods run under kubepods and the shims that manage them run in the service's
// own cgroup, so with KillMode=process neither is killed when the service stops.
// A caller that then deletes the data directory strands both: the containers
// keep running, and writing, against files that are no longer there. Reset and
// uninstall call this; restart and upgrade must not, since there the workloads
// are meant to survive and be reattached to.
//
// Call it only after the service is stopped. Anything still in its cgroup by
// then is an orphan.
func StopWorkloads() {
	pids := podPIDs()
	if len(pids) == 0 {
		log.Debug().Msg("no pod processes found")
		return
	}

	log.Info().Msgf("stopping %d pod process(es) via SIGTERM...", len(pids))
	for _, pid := range pids {
		signalPID(pid, syscall.SIGTERM)
	}

	backoff := time.Second
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(backoff)
		backoff = min(backoff*2, 4*time.Second)
		if remaining := podPIDs(); len(remaining) == 0 {
			log.Info().Msg("pod processes stopped cleanly")
			return
		}
	}

	remaining := podPIDs()
	if len(remaining) > 0 {
		log.Warn().Msgf("force-killing %d pod process(es) with SIGKILL...", len(remaining))
		for _, pid := range remaining {
			signalPID(pid, syscall.SIGKILL)
		}
		time.Sleep(time.Second)
	}

	if still := podPIDs(); len(still) > 0 {
		log.Warn().Msgf("some pod processes may still be running (%v) — continuing anyway", still)
	} else {
		log.Info().Msg("pod processes stopped")
	}
}

// podPIDs collects the PIDs still running in the kubepods cgroups and in the
// stopped service's own cgroup, deduplicated. Under cgroup v1 the same process
// appears once per subsystem.
func podPIDs() []int {
	seen := map[int]struct{}{}
	var pids []int

	roots := append(kubepodsCgroupRoots(), serviceCgroupRoots()...)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "cgroup.procs" {
				return nil //nolint:nilerr // an unreadable cgroup must not abort the walk
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, line := range strings.Fields(string(raw)) {
				pid, err := strconv.Atoi(line)
				if err != nil || pid == os.Getpid() {
					continue
				}
				if _, dup := seen[pid]; dup {
					continue
				}
				seen[pid] = struct{}{}
				pids = append(pids, pid)
			}
			return nil
		})
	}
	return pids
}

// serviceCgroupRoots returns the KubeSolo service cgroups. Once systemd has
// stopped the unit, what remains in here is the shims KillMode=process spared.
// Matching on the unit name keeps a containerd managed by the host, which runs
// its shims under its own unit, out of range.
func serviceCgroupRoots() []string {
	matches, _ := filepath.Glob("/sys/fs/cgroup/system.slice/kubesolo*.service")
	return matches
}

// kubepodsCgroupRoots returns the kubepods cgroups present on this host. The
// name depends on the kubelet's cgroup driver: "kubepods.slice" for systemd,
// "kubepods" for cgroupfs. The starred patterns match cgroup v1, which mounts
// one hierarchy per subsystem.
func kubepodsCgroupRoots() []string {
	patterns := []string{
		"/sys/fs/cgroup/kubepods.slice",
		"/sys/fs/cgroup/kubepods",
		"/sys/fs/cgroup/*/kubepods.slice",
		"/sys/fs/cgroup/*/kubepods",
	}

	var roots []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		roots = append(roots, matches...)
	}
	return roots
}
