package service

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// A container upgrade keeps one backup in the data volume, as a host upgrade
// keeps one in the data directory: the datastore as it was before the upgrade,
// and target.json, the image and command line that ran on it. While an upgrade
// is unproven, the backup it replaced is kept beside it, so that a failed
// upgrade leaves the earlier rollback target in place.
const (
	containerDataDir = "/var/lib/kubesolo"
	backupDir        = containerDataDir + "/upgrade/container-backup"
)

// RollbackTarget is what a container upgrade replaced, and what a rollback
// restores.
type RollbackTarget struct {
	Image   string    `json:"image"`
	Args    []string  `json:"args"`
	Version string    `json:"version,omitempty"`
	Created time.Time `json:"created"`
}

// StopContainer stops the named instance's container, giving KubeSolo time to
// shut down cleanly.
func StopContainer(name string) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	timeout := 30
	return cli.ContainerStop(context.Background(), ContainerNameFor(name), container.StopOptions{Timeout: &timeout})
}

// StartContainer starts the named instance's container.
func StartContainer(name string) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	return cli.ContainerStart(context.Background(), ContainerNameFor(name), container.StartOptions{})
}

// BackupDatastore copies the datastore of the named instance, whose container
// must be stopped, into the backup, with target describing how to return to
// it. image runs the copy; it is the image of the stopped container.
func BackupDatastore(name, image string, target RollbackTarget) error {
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	return runInVolume(name, image, `set -e
b=`+backupDir+`
rm -rf "$b.new"
mkdir -p "$b.new"
cp -a `+containerDataDir+`/kine "$b.new/kine"
printf '%s' "$TARGET" > "$b.new/target.json"
rm -rf "$b.prev"
if [ -d "$b" ]; then mv "$b" "$b.prev"; fi
mv "$b.new" "$b"`, "TARGET="+string(data))
}

// RestoreDatastore puts a copy of the backup's datastore in place of the named
// instance's, whose container must be stopped. The backup itself is kept, so
// that a restored container which fails to come up can be rolled back again;
// FinishRestore drops it once the restored container is healthy.
func RestoreDatastore(name, image string) error {
	return runInVolume(name, image, `set -e
b=`+backupDir+`
test -f "$b/kine/db/state.db"
rm -rf `+containerDataDir+`/kine
cp -a "$b/kine" `+containerDataDir+`/kine`)
}

// FinishRestore drops the backup a healthy restored container now runs. undo
// is true when the upgrade that took the backup failed: the backup it replaced
// becomes the rollback target again.
func FinishRestore(name, image string, undo bool) error {
	script := `set -e
b=` + backupDir + `
rm -rf "$b"
`
	if undo {
		script += `if [ -d "$b.prev" ]; then mv "$b.prev" "$b"; fi`
	} else {
		script += `rm -rf "$b.prev"`
	}
	return runInVolume(name, image, script)
}

// SettleBackup drops the backup an upgrade replaced, once the upgrade has
// proven healthy.
func SettleBackup(name, image string) error {
	return runInVolume(name, image, `rm -rf `+backupDir+`.prev`)
}

// ReadRollbackTarget returns the named instance's rollback target, or nil when
// it has none.
func ReadRollbackTarget(name string) (*RollbackTarget, error) {
	cli, err := newContainerClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	rc, _, err := cli.CopyFromContainer(context.Background(), ContainerNameFor(name), backupDir+"/target.json")
	if cerrdefs.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("failed to read the rollback target: %w", err)
	}
	var target RollbackTarget
	if err := json.NewDecoder(tr).Decode(&target); err != nil {
		return nil, fmt.Errorf("failed to read the rollback target: %w", err)
	}
	return &target, nil
}

// runInVolume runs script with sh in a throwaway container of image, with the
// named instance's data volume mounted where KubeSolo has it.
func runInVolume(name, image, script string, env ...string) error {
	cli, err := newContainerClient()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	ctx := context.Background()
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:      image,
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{script},
			Env:        env,
		},
		&container.HostConfig{
			Binds:       []string{ContainerNameFor(name) + "-data:" + containerDataDir},
			NetworkMode: "none",
		},
		nil, nil, "")
	if err != nil {
		return fmt.Errorf("failed to create a container to work on the data volume: %w", err)
	}
	defer func() {
		_ = cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	}()

	waitCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNextExit)
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start a container to work on the data volume: %w", err)
	}
	select {
	case err := <-errCh:
		return err
	case st := <-waitCh:
		if st.StatusCode == 0 {
			return nil
		}
		out := logsOf(ctx, cli, resp.ID)
		return fmt.Errorf("working on the data volume failed (exit code %d): %s", st.StatusCode, out)
	}
}

func logsOf(ctx context.Context, cli *client.Client, id string) string {
	rc, err := cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, rc); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	return strings.TrimSpace(out.String())
}
