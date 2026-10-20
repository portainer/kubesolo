package assistant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, mib int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, mib<<20), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The snapshot of a full disk says what fills it, so the model can tell
// pruning images from freeing space elsewhere.
func TestDiskUsageByCategory(t *testing.T) {
	data := t.TempDir()
	logs := t.TempDir()
	model := filepath.Join(data, "containerd/root/io.containerd.content.v1.content/blobs/sha256/model")
	writeFile(t, model, 3)
	writeFile(t, filepath.Join(data, "containerd/root/io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs/bin"), 4)
	writeFile(t, filepath.Join(logs, "default_web/web/0.log"), 2)
	writeFile(t, filepath.Join(data, "kubelet/pods/uid/volumes/kubernetes.io~empty-dir/cache/data"), 1)
	writeFile(t, filepath.Join(data, "local-path-storage/pvc-1/db"), 1)
	writeFile(t, filepath.Join(data, "kine/db/state.db"), 1)
	writeFile(t, filepath.Join(data, "fill"), 5)

	got := diskUsage(context.Background(), diskLayout{dataDir: data, podLogsDir: logs, modelFile: model})

	for category, want := range map[string]int64{
		"images": 4, "assistantModel": 3, "podLogs": 2, "volumes": 2, "datastore": 1,
	} {
		if got[category] != want {
			t.Errorf("%s = %d MiB, want %d (all: %v)", category, got[category], want, got)
		}
	}
	// "other" is the rest of the filesystem: at least the fill, and what else
	// the test host holds there.
	if got["other"] < 5 {
		t.Errorf("other = %d MiB, want at least the 5 MiB fill", got["other"])
	}
}

// Walking a large disk can take long; a breakdown that runs out of time is
// left out rather than given wrong.
func TestDiskUsageGivesUpInTime(t *testing.T) {
	data := t.TempDir()
	writeFile(t, filepath.Join(data, "kine/db/state.db"), 1)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	if got := diskUsage(ctx, diskLayout{dataDir: data}); got != nil {
		t.Errorf("got %v, want nothing once out of time", got)
	}
}
