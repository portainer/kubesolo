package service

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/rs/zerolog/log"
)

const openrcServicePath = "/etc/init.d/kubesolo"

const openrcTemplate = `#!/sbin/openrc-run
{{- if .Proxy}}
export HTTP_PROXY="{{.Proxy}}"
export HTTPS_PROXY="{{.Proxy}}"
export NO_PROXY="localhost,127.0.0.1"
{{- end}}

name="{{.AppName}}"
description="KubeSolo single-node Kubernetes distribution"
command="{{.InstallPath}}"
command_args="{{.CmdArgs}}"
command_background=true
pidfile="/var/run/${RC_SVCNAME}.pid"
command_user="root"

depend() {
    need net
    after firewall
}
`

type openrcManager struct{}

func (m *openrcManager) Install(cfg *config.Config, cmdArgs []string) error {
	data := buildTemplateData(cfg, cmdArgs)
	script, err := renderTemplate("openrc", openrcTemplate, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(openrcServicePath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("failed to write OpenRC init script: %w", err)
	}
	log.Info().Msgf("wrote OpenRC init script: %s", openrcServicePath)

	if err := rcUpdate("add", config.AppName, "default"); err != nil {
		return fmt.Errorf("rc-update add: %w", err)
	}
	if err := rcService(config.AppName, "start"); err != nil {
		return fmt.Errorf("rc-service start: %w", err)
	}
	log.Info().Msg("KubeSolo service installed and started via OpenRC")
	return nil
}

func (m *openrcManager) Uninstall() error {
	_ = rcService(config.AppName, "stop")
	_ = rcUpdate("del", config.AppName, "default")
	_ = os.Remove(openrcServicePath)
	log.Info().Msg("KubeSolo OpenRC service removed")
	return nil
}

func rcUpdate(args ...string) error {
	out, err := exec.Command("rc-update", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return nil
}

func rcService(args ...string) error {
	out, err := exec.Command("rc-service", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return nil
}
