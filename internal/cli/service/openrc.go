package service

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

const openrcServicePath = "/etc/init.d/kubesolo"

const openrcTemplate = `#!/sbin/openrc-run
{{- if .Proxy}}
export HTTP_PROXY={{.Proxy | shellQuote}}
export HTTPS_PROXY={{.Proxy | shellQuote}}
export NO_PROXY='localhost,127.0.0.1'
{{- end}}

name="{{.AppName}}"
description="KubeSolo single-node Kubernetes distribution"
command="{{.InstallPath}}"
command_args="{{range .CmdArgsList}}{{. | shellQuote}} {{end}}"
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

	if err := runRCUpdate("add", config.AppName, "default"); err != nil {
		return fmt.Errorf("rc-update add: %w", err)
	}
	if err := runRCService(config.AppName, "start"); err != nil {
		return fmt.Errorf("rc-service start: %w", err)
	}
	log.Info().Msg("KubeSolo service installed and started via OpenRC")
	return nil
}

func (m *openrcManager) Uninstall() error {
	_ = runRCService(config.AppName, "stop")
	_ = runRCUpdate("del", config.AppName, "default")
	_ = os.Remove(openrcServicePath)
	log.Info().Msg("KubeSolo OpenRC service removed")
	return nil
}

func runRCUpdate(args ...string) error {
	out, err := exec.Command("rc-update", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return nil
}

func runRCService(args ...string) error {
	out, err := exec.Command("rc-service", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return nil
}
