package service

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

type daemonManager struct{}

func (m *daemonManager) Install(cfg *config.Config, cmdArgs []string) error {
	logFile := config.LogFile
	pidFile := config.PIDFile

	if err := os.MkdirAll("/var/log", 0o755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Build the command with proxy environment if required
	env := os.Environ()
	if cfg.Proxy != "" {
		env = append(env,
			"HTTP_PROXY="+cfg.Proxy,
			"HTTPS_PROXY="+cfg.Proxy,
			"NO_PROXY=localhost,127.0.0.1",
		)
	}

	log.Info().Msgf("starting KubeSolo as background daemon (logs: %s)...", logFile)

	logFH, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", logFile, err)
	}

	// os.StartProcess calls Fd() on every Files entry; nil panics at runtime.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		logFH.Close()
		return fmt.Errorf("failed to open %s: %w", os.DevNull, err)
	}

	args := append([]string{config.DefaultInstallPath}, cmdArgs...)
	proc, err := os.StartProcess(config.DefaultInstallPath, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{devNull, logFH, logFH},
		Sys:   daemonSysProcAttr(),
	})
	devNull.Close()
	logFH.Close()
	if err != nil {
		return fmt.Errorf("failed to start KubeSolo daemon: %w", err)
	}
	// Detach — let the child run independently
	if err := proc.Release(); err != nil {
		return fmt.Errorf("failed to release daemon process: %w", err)
	}

	pid := proc.Pid
	// PID file write failure is non-fatal: the daemon is already running at this
	// point, so returning an error would leave the system in an inconsistent state.
	// A warning is enough — Uninstall() cross-checks /proc/<pid>/exe rather than
	// the PID file as the authoritative source of truth.
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		log.Warn().Err(err).Msgf("failed to write PID file %s", pidFile)
	}

	log.Info().Msgf("KubeSolo started as daemon (PID %d)", pid)
	log.Info().Msgf("logs:    tail -f %s", logFile)
	log.Info().Msgf("stop:    kill $(cat %s)", pidFile)
	return nil
}

func (m *daemonManager) Uninstall() error {
	data, err := os.ReadFile(config.PIDFile)
	if err != nil {
		return nil // no PID file, nothing to stop
	}
	pidStr := strings.TrimSpace(string(data))
	var pid int
	if _, err := fmt.Sscan(pidStr, &pid); err != nil || pid <= 0 {
		_ = os.Remove(config.PIDFile)
		return nil
	}

	// Verify the PID belongs to the KubeSolo binary before signalling.
	// A recycled PID pointing to an unrelated process must not be killed.
	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := os.Readlink(exePath)
	if err != nil || target != config.DefaultInstallPath {
		log.Debug().Msgf("PID %d in pid file does not resolve to KubeSolo (exe: %q) — skipping signal", pid, target)
		_ = os.Remove(config.PIDFile)
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(config.PIDFile)
		return nil
	}

	// SIGTERM first; give the process up to 5 s to exit cleanly.
	_ = proc.Signal(syscall.SIGTERM)
	exited := false
	for range 5 {
		time.Sleep(time.Second)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process no longer exists
			exited = true
			break
		}
	}
	if !exited {
		_ = proc.Signal(syscall.SIGKILL)
	}

	_ = os.Remove(config.PIDFile)
	log.Info().Msgf("KubeSolo daemon (PID %d) stopped", pid)
	return nil
}
