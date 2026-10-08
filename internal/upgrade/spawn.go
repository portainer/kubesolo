package upgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/portainer/kubesolo/internal/cli/detect"
)

// ExecutorFlag runs the kubesolo binary as the upgrade executor. Its value is a
// job file.
const ExecutorFlag = "--upgrade-executor"

// CheckDatastoreFlag runs the kubesolo binary as the datastore dry-run. Its
// value is the copy of the datastore to migrate and check.
const CheckDatastoreFlag = "--upgrade-check-datastore"

// Job is what the executor is asked to do. It is written to a file under the
// upgrade directory and the executor is pointed at it, so no part of the
// request depends on surviving a command line.
type Job struct {
	ID        string    `json:"id"`
	Operation Operation `json:"operation"`
	Request   Request   `json:"request"`

	DataDir    string `json:"dataDir"`
	ConfigFile string `json:"configFile"`

	// From is the version running when the job was accepted.
	From string `json:"from"`
}

// Layout returns the job's layout.
func (j Job) Layout() Layout { return NewLayout(j.DataDir, j.ConfigFile) }

// JobFile is where a job is written.
func (l Layout) JobFile(id string) string { return filepath.Join(l.Dir(), "job-"+id+".json") }

// WriteJob writes the job file.
func WriteJob(l Layout, j Job) (string, error) {
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return "", err
	}
	path := l.JobFile(j.ID)
	return path, WriteFileAtomic(path, raw, 0o600)
}

// ReadJob reads a job file.
func ReadJob(path string) (Job, error) {
	var j Job
	raw, err := os.ReadFile(path)
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, fmt.Errorf("job %s is unreadable: %w", path, err)
	}
	return j, nil
}

// SpawnExecutor starts `exe --upgrade-executor=<jobFile>` so that it outlives
// the KubeSolo service it is about to stop.
//
// The caller may be KubeSolo itself (the API), and stopping KubeSolo stops
// everything in its service. Under systemd the executor runs as its own
// transient unit; elsewhere it starts a new session, and moves itself out of
// the service's cgroup once running (EscapeServiceCgroup).
func SpawnExecutor(exe, jobFile string, init detect.InitSystem) error {
	arg := ExecutorFlag + "=" + jobFile
	env := executorEnv()

	if init == detect.InitSystemd {
		if path, err := exec.LookPath("systemd-run"); err == nil {
			id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(jobFile), "job-"), ".json")
			args := []string{
				"--unit=kubesolo-upgrade-" + id,
				"--description=KubeSolo upgrade " + id,
				"--collect", "--quiet",
				"--property=KillMode=process",
			}
			for _, kv := range env {
				args = append(args, "--setenv="+kv)
			}
			args = append(args, exe, arg)
			out, err := exec.Command(path, args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("systemd-run: %w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()
	proc, err := os.StartProcess(exe, []string{exe, arg}, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{devNull, devNull, devNull},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return fmt.Errorf("start the upgrade executor: %w", err)
	}
	return proc.Release()
}

// executorEnv is the environment the executor gets: a sane PATH, and the
// settings that decide where a release is downloaded from.
func executorEnv() []string {
	env := baseEnv()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "KUBESOLO_RELEASE_BASE_URL"} {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// EscapeServiceCgroup moves the calling process to the root cgroup, where
// stopping the KubeSolo service cannot reach it. Init systems that track a
// service by cgroup (OpenRC with rc_cgroup_cleanup, s6 and runit with cgroup
// supervision) kill everything left in it on stop; a new session is not
// enough. It is best effort: without permission or cgroups there is nothing to
// escape.
func EscapeServiceCgroup() {
	moveToRootCgroup(os.Getpid())
}

// moveToRootCgroup moves a process to the root cgroup, best effort.
func moveToRootCgroup(p int) {
	pid := []byte(fmt.Sprintf("%d\n", p))
	// cgroup v2: one hierarchy.
	if err := os.WriteFile("/sys/fs/cgroup/cgroup.procs", pid, 0); err == nil {
		return
	}
	// cgroup v1: one hierarchy per controller.
	dirs, _ := filepath.Glob("/sys/fs/cgroup/*/cgroup.procs")
	for _, procs := range dirs {
		_ = os.WriteFile(procs, pid, 0)
	}
}

// UnderSystemdUnit reports whether the process was started by systemd as a
// service, as SpawnExecutor's transient unit is.
func UnderSystemdUnit() bool {
	return os.Getenv("INVOCATION_ID") != ""
}
