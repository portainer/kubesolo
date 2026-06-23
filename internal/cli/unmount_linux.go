package cli

import (
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/rs/zerolog/log"
)

// unmountDataDir detaches all filesystems mounted under path before removal.
// containerd leaves overlay and bind mounts active under the data directory;
// os.RemoveAll fails on a locked filesystem without this step.
// MNT_DETACH (lazy unmount) lets the kernel clean up once the last user exits.
func unmountDataDir(path string) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return
	}

	var mounts []string
	prefix := path + "/"
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mp := fields[1]
		if mp == path || strings.HasPrefix(mp, prefix) {
			mounts = append(mounts, mp)
		}
	}
	if len(mounts) == 0 {
		return
	}

	// Reverse-sort so deepest (longest) paths are unmounted before their parents.
	sort.Sort(sort.Reverse(sort.StringSlice(mounts)))

	log.Info().Msgf("unmounting %d filesystem(s) under %s...", len(mounts), path)
	for _, mp := range mounts {
		log.Debug().Msgf("unmounting %s", mp)
		if err := syscall.Unmount(mp, syscall.MNT_DETACH); err != nil {
			log.Warn().Err(err).Msgf("failed to unmount %s", mp)
		}
	}
}
