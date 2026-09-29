package service

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

const systemdServicePath = "/etc/systemd/system/kubesolo.service"

const systemdUnitTemplate = `[Unit]
Description=KubeSolo single-node Kubernetes distribution
After=network.target

[Service]
ExecStart={{.InstallPath}}{{range .CmdArgsList}} {{. | shellQuote}}{{end}}
Restart=always
RestartSec=3
# Shims start in this unit's cgroup but manage containers in kubepods.slice.
# The default KillMode=control-group kills them on stop, so the next start
# cannot reattach and kubelet duplicates every pod (issue #197). Matches the
# unit containerd ships upstream.
KillMode=process
Delegate=yes
OOMScoreAdjust=-500
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal
{{- if .Proxy}}
Environment="HTTP_PROXY={{.Proxy | systemdEnvVal}}"
Environment="HTTPS_PROXY={{.Proxy | systemdEnvVal}}"
Environment="NO_PROXY=localhost,127.0.0.1"
{{- end}}

[Install]
WantedBy=multi-user.target
`

type systemdManager struct{}

func (m *systemdManager) Install(cfg *config.Config, cmdArgs []string) error {
	data := buildTemplateData(cfg, cmdArgs)
	unit, err := renderTemplate("systemd", systemdUnitTemplate, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(systemdServicePath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("failed to write systemd unit file: %w", err)
	}
	log.Info().Msgf("wrote systemd unit: %s", systemdServicePath)

	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", config.AppName},
		{"restart", config.AppName},
	} {
		if err := systemctl(args...); err != nil {
			return fmt.Errorf("systemctl %v: %w", args, err)
		}
	}
	log.Info().Msg("KubeSolo service installed and started via systemd")
	return nil
}

func (m *systemdManager) Uninstall() error {
	_ = systemctl("stop", config.AppName)
	_ = systemctl("disable", config.AppName)
	_ = os.Remove(systemdServicePath)
	_ = systemctl("daemon-reload")
	log.Info().Msg("KubeSolo systemd service removed")
	return nil
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return nil
}
