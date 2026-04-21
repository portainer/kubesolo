package service

import (
	"fmt"
	"os"
	"strings"

	"github.com/portainer/kubesolo/internal/installer/config"
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

	args := append([]string{config.DefaultInstallPath}, cmdArgs...)
	proc, err := os.StartProcess(config.DefaultInstallPath, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{nil, logFH, logFH},
		Sys:   daemonSysProcAttr(),
	})
	logFH.Close()
	if err != nil {
		return fmt.Errorf("failed to start KubeSolo daemon: %w", err)
	}
	// Detach — let the child run independently
	if err := proc.Release(); err != nil {
		return fmt.Errorf("failed to release daemon process: %w", err)
	}

	pid := proc.Pid
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
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	_ = proc.Kill()
	_ = os.Remove(config.PIDFile)
	log.Info().Msgf("KubeSolo daemon (PID %d) stopped", pid)
	return nil
}
