package assistant

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// diskLayout is where KubeSolo keeps what fills its disk.
type diskLayout struct {
	dataDir    string
	podLogsDir string

	// modelFile is the assistant's model, kept in containerd's store beside
	// the images but not pruned with them; "" when there is none.
	modelFile string
}

// diskUsage breaks down the used space of the filesystem holding dataDir, in
// MiB: images, the assistant's model, pod logs, volumes, the datastore, and
// everything else. A directory on another filesystem is not counted. It
// returns nil if the filesystem cannot be read or ctx ends first: a partial
// breakdown would mislead.
func diskUsage(ctx context.Context, l diskLayout) map[string]int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(l.dataDir, &st) != nil {
		return nil
	}
	used := int64(st.Blocks-st.Bfree) * st.Bsize
	dev, ok := device(l.dataDir)
	if !ok {
		return nil
	}

	bytes := map[string]int64{}
	var counted int64
	for _, c := range []struct {
		name string
		dirs []string
	}{
		{"images", []string{filepath.Join(l.dataDir, "containerd", "root")}},
		{"podLogs", []string{l.podLogsDir}},
		{"volumes", []string{filepath.Join(l.dataDir, "kubelet", "pods"), filepath.Join(l.dataDir, "local-path-storage")}},
		{"datastore", []string{filepath.Join(l.dataDir, "kine")}},
	} {
		for _, dir := range c.dirs {
			n, err := dirSize(ctx, dir, dev)
			if err != nil {
				return nil
			}
			bytes[c.name] += n
			counted += n
		}
	}
	if l.modelFile != "" {
		if n, ok := fileSize(l.modelFile); ok {
			bytes["images"] -= n
			bytes["assistantModel"] = n
		}
	}
	bytes["other"] = max(0, used-counted)

	out := make(map[string]int64, len(bytes))
	for name, n := range bytes {
		out[name] = n >> 20
	}
	return out
}

// dirSize adds up the space the regular files under dir take on the device,
// stopping with ctx's error once it ends. A missing dir takes none.
func dirSize(ctx context.Context, dir string, dev uint64) (int64, error) {
	if dir == "" {
		return 0, nil
	}
	if d, ok := device(dir); !ok || d != dev {
		return 0, nil
	}
	var total int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil || !d.Type().IsRegular() {
			return nil // unreadable entries are skipped, not fatal
		}
		if n, ok := fileSize(path); ok {
			total += n
		}
		return nil
	})
	return total, err
}

// fileSize is the space a file takes on disk, which for a sparse or
// preallocated file differs from its length.
func fileSize(path string) (int64, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Blocks * 512, true
}

func device(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // Dev's type differs between architectures
}
