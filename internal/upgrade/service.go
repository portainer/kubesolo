package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/portainer/kubesolo/internal/cli/detect"
)

// Service definition paths, as both kubesoloctl and install.sh write them.
const (
	systemdUnit = "/etc/systemd/system/kubesolo.service"
	initDScript = "/etc/init.d/kubesolo"
	s6Dir       = "/etc/s6/sv/kubesolo"
	runitDir    = "/etc/runit/sv/kubesolo"
	upstartJob  = "/etc/init/kubesolo.conf"

	// daemonPIDFile and daemonLogFile are what daemon mode uses, which has no
	// supervisor and no service definition.
	daemonPIDFile = "/var/run/kubesolo.pid"
	daemonLogFile = "/var/log/kubesolo.log"
)

// ErrContainerMode is returned on a KubeSolo running in a container. The
// container cannot replace its own image; kubesoloctl upgrades it from the host.
var ErrContainerMode = errors.New("KubeSolo is running in a container, which cannot upgrade itself; run `kubesoloctl upgrade` on the host instead")

// Service stops and starts KubeSolo the way it was installed.
type Service struct {
	Init detect.InitSystem

	// Daemon is set for daemon mode: KubeSolo started in the background with no
	// supervisor. It is restarted with the arguments and proxy settings the
	// running process was started with.
	Daemon bool
	argv   []string
	env    []string

	binary string
}

// String names how KubeSolo is managed, for messages.
func (s *Service) String() string {
	if s.Daemon {
		return "daemon"
	}
	return string(s.Init)
}

// DetectService works out how KubeSolo is run on this host. A service
// definition for the detected init system wins; failing that, a running
// KubeSolo with no definition is a daemon.
func DetectService(l Layout, info *detect.SystemInfo) (*Service, error) {
	if info.Environment == detect.EnvContainer {
		return nil, ErrContainerMode
	}
	s := &Service{Init: info.InitSystem, binary: l.Binary}

	if def := definitionPath(info.InitSystem); def != "" {
		if _, err := os.Stat(def); err == nil {
			return s, nil
		}
	}

	pids := MainPIDs(l.Binary)
	if len(pids) == 0 {
		return nil, fmt.Errorf("no KubeSolo service is installed for %s, and no KubeSolo process is running", info.InitSystem)
	}
	argv, err := readNulFile(fmt.Sprintf("/proc/%d/cmdline", pids[0]))
	if err != nil || len(argv) == 0 {
		return nil, fmt.Errorf("read the running KubeSolo's arguments: %w", err)
	}
	env, _ := readNulFile(fmt.Sprintf("/proc/%d/environ", pids[0]))
	s.Daemon, s.argv, s.env = true, argv, proxyEnv(env)
	return s, nil
}

// DefinitionFile is the file that defines how the init system runs KubeSolo,
// or "" in daemon mode, which has none. The upgrade backs it up, because
// what happens after an upgrade — the move from flags to a configuration file
// — rewrites it into a form an older release cannot run.
func (s *Service) DefinitionFile() string {
	if s.Daemon {
		return ""
	}
	switch s.Init {
	case detect.InitS6:
		return filepath.Join(s6Dir, "run")
	case detect.InitRunit:
		return filepath.Join(runitDir, "run")
	}
	return definitionPath(s.Init)
}

// ReloadDefinitions makes the init system reread a service definition that has
// changed on disk. Only systemd caches them.
func (s *Service) ReloadDefinitions() error {
	if s.Daemon || s.Init != detect.InitSystemd {
		return nil
	}
	out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func definitionPath(init detect.InitSystem) string {
	switch init {
	case detect.InitSystemd:
		return systemdUnit
	case detect.InitOpenRC, detect.InitSysV:
		return initDScript
	case detect.InitS6:
		return s6Dir
	case detect.InitRunit:
		return runitDir
	case detect.InitUpstart:
		return upstartJob
	}
	return ""
}

func readNulFile(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimRight(raw, "\x00")
	if len(raw) == 0 {
		return nil, nil
	}
	return strings.Split(string(raw), "\x00"), nil
}

// proxyEnv keeps the proxy settings from a process environment, the only part
// of it KubeSolo's behaviour depends on.
func proxyEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
			out = append(out, kv)
		}
	}
	return out
}

// stopTimeout bounds a graceful stop. KubeSolo shuts down in seconds; anything
// still running after this is killed.
const stopTimeout = 60 * time.Second

// Stop stops KubeSolo and waits for its process to exit. Containers keep
// running: the shims survive the service (KillMode=process) and the next
// KubeSolo reattaches to them.
func (s *Service) Stop(ctx context.Context) error {
	pids := MainPIDs(s.binary)

	// The init system's stop blocks until the process exits, so it runs
	// alongside the nudge below rather than before it. An error is not fatal:
	// if the init system could not stop it, the process is signalled.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if s.Daemon {
			for _, pid := range pids {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
			return
		}
		_ = s.control("stop")
	}()

	// Releases up to v1.2.1 only start shutting down on a second SIGTERM: the
	// first is taken by a goroutine that cancels everything, while main waits
	// for another. Left alone they sit out the init system's stop timeout and
	// are killed mid-write. A second SIGTERM lets them shut down cleanly; a
	// release that has already exited, or is exiting, ignores it.
	if !waitForExit(ctx, s.binary, secondSignalAfter) {
		for _, pid := range pids {
			if processAlive(pid) {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	}

	if waitForExit(ctx, s.binary, stopTimeout) {
		<-done
		if s.Daemon {
			_ = os.Remove(daemonPIDFile)
		}
		return nil
	}
	for _, pid := range MainPIDs(s.binary) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	if waitForExit(ctx, s.binary, 10*time.Second) {
		<-done
		return nil
	}
	return fmt.Errorf("KubeSolo did not stop (pids %v)", MainPIDs(s.binary))
}

// secondSignalAfter is how long a stop waits before sending a second SIGTERM.
const secondSignalAfter = 5 * time.Second

// Start starts KubeSolo.
func (s *Service) Start() error {
	if s.Daemon {
		return s.startDaemon()
	}
	return s.control("start")
}

func (s *Service) control(action string) error {
	var cmd []string
	switch s.Init {
	case detect.InitSystemd:
		cmd = []string{"systemctl", action, "kubesolo"}
	case detect.InitOpenRC:
		cmd = []string{"rc-service", "kubesolo", action}
	case detect.InitSysV:
		cmd = []string{initDScript, action}
	case detect.InitUpstart:
		cmd = []string{"initctl", action, "kubesolo"}
	case detect.InitS6:
		flag := map[string]string{"start": "-u", "stop": "-d"}[action]
		cmd = []string{"s6-svc", flag, s6Dir}
	case detect.InitRunit:
		verb := map[string]string{"start": "up", "stop": "down"}[action]
		cmd = []string{"sv", verb, runitDir}
	default:
		return fmt.Errorf("cannot %s KubeSolo: unsupported init system %q", action, s.Init)
	}
	out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(cmd, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *Service) startDaemon() error {
	logFH, err := os.OpenFile(daemonLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logFH.Close() }()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()

	env := append(baseEnv(), s.env...)
	proc, err := os.StartProcess(s.binary, append([]string{s.binary}, s.argv[1:]...), &os.ProcAttr{
		Env:   env,
		Files: []*os.File{devNull, logFH, logFH},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return fmt.Errorf("start KubeSolo daemon: %w", err)
	}
	// A daemon belongs to no service. Left where it was started, it would live in
	// the executor's cgroup — a transient unit under systemd — and outlive it
	// there as an orphan of that unit.
	moveToRootCgroup(proc.Pid)
	if err := os.WriteFile(daemonPIDFile, []byte(strconv.Itoa(proc.Pid)+"\n"), 0o644); err != nil {
		return err
	}
	return proc.Release()
}

// baseEnv is a minimal environment for a process started outside any service
// manager.
func baseEnv() []string {
	return []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root"}
}

// MainPIDs returns the running KubeSolo processes started from binary: those
// whose executable is binary, including one that has since been replaced on
// disk. The upgrade's own processes, which may run from the same binary, are
// excluded, as is the caller.
func MainPIDs(binary string) []int {
	entries, _ := filepath.Glob("/proc/[0-9]*/exe")
	self := os.Getpid()
	var pids []int
	for _, link := range entries {
		target, err := os.Readlink(link)
		if err != nil || strings.TrimSuffix(target, " (deleted)") != binary {
			continue
		}
		dir := filepath.Dir(link)
		pid, err := strconv.Atoi(filepath.Base(dir))
		if err != nil || pid == self {
			continue
		}
		if argv, _ := readNulFile(filepath.Join(dir, "cmdline")); isUpgradeHelper(argv) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// isUpgradeHelper reports whether argv is a kubesolo running as the upgrade
// executor or the datastore dry-run, rather than as KubeSolo.
func isUpgradeHelper(argv []string) bool {
	for _, a := range argv {
		if strings.HasPrefix(a, "--upgrade-executor") || strings.HasPrefix(a, "--upgrade-check-datastore") {
			return true
		}
	}
	return false
}

func waitForExit(ctx context.Context, binary string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if len(MainPIDs(binary)) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
}
