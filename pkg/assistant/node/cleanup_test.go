package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Only images no container uses, running or not, are pruned, and never a
// pinned one such as the pause image.
func TestUnusedImages(t *testing.T) {
	images := []*runtimeapi.Image{
		{Id: "sha256:web", RepoTags: []string{"shop/web:1"}},
		{Id: "sha256:old", RepoTags: []string{"shop/web:0"}},
		{Id: "sha256:stopped", RepoTags: []string{"shop/job:3"}},
		{Id: "sha256:pause", RepoTags: []string{"portainer/pause:latest"}, Pinned: true},
	}
	containers := []*runtimeapi.Container{
		{ImageRef: "sha256:web", State: runtimeapi.ContainerState_CONTAINER_RUNNING},
		{ImageRef: "sha256:stopped", State: runtimeapi.ContainerState_CONTAINER_EXITED},
	}

	got := unusedImages(images, containers)
	if len(got) != 1 || got[0].Id != "sha256:old" {
		t.Errorf("unused = %v, want only sha256:old", got)
	}
}

// containerCgroups writes a pod cgroup of the systemd driver holding a
// cgroup, with a process, for each container ID, and one left empty.
func containerCgroups(t *testing.T, ids ...string) string {
	t.Helper()
	root := t.TempDir()
	pod := filepath.Join(root, "kubepods.slice", "kubepods-besteffort.slice",
		"kubepods-besteffort-pod11111111_1111_1111_1111_111111111111.slice")
	for _, id := range ids {
		withProcess(t, filepath.Join(pod, "cri-containerd-"+id+".scope"))
	}
	if err := os.MkdirAll(filepath.Join(pod, "cri-containerd-"+cid('e')+".scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func withProcess(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte("4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cid is a container ID of 64 c's.
func cid(c byte) string { return strings.Repeat(string(c), 64) }

// An orphan is a container cgroup still holding processes while the runtime
// has no live container or sandbox by its ID: a container it reports exited,
// or has lost track of, whose process runs on beside the copy the kubelet
// started instead. The kubelet itself reaps whole pods it does not know.
func TestOrphanedContainers(t *testing.T) {
	running, sandbox, exited := cid('a'), cid('b'), cid('c')
	root := containerCgroups(t, running, sandbox, exited)

	got, err := orphanedContainers(root, []string{running, sandbox})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].id != exited {
		t.Errorf("orphans = %+v, want only %s", got, exited)
	}
}

// The cgroupfs driver names container cgroups by their bare ID, under
// kubepods/<class>/pod<uid>.
func TestOrphanedContainersUnderCgroupfs(t *testing.T) {
	root := t.TempDir()
	withProcess(t, filepath.Join(root, "kubepods", "burstable", "pod33333333-3333-3333-3333-333333333333", cid('d')))

	got, err := orphanedContainers(root, nil)
	if err != nil || len(got) != 1 || got[0].id != cid('d') {
		t.Errorf("orphans = %+v, %v", got, err)
	}
}
