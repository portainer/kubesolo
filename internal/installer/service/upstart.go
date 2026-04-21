package service

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/rs/zerolog/log"
)

const upstartConfPath = "/etc/init/kubesolo.conf"

const upstartTemplate = `description "KubeSolo single-node Kubernetes distribution"
author "Portainer"

start on runlevel [2345]
stop on runlevel [!2345]

respawn
respawn limit 10 5
{{- if .Proxy}}
env HTTP_PROXY={{.Proxy | shellQuote}}
env HTTPS_PROXY={{.Proxy | shellQuote}}
env NO_PROXY=localhost,127.0.0.1
{{- end}}

exec {{.InstallPath}}{{range .CmdArgsList}} {{. | shellQuote}}{{end}}
`

type upstartManager struct{}

func (m *upstartManager) Install(cfg *config.Config, cmdArgs []string) error {
	data := buildTemplateData(cfg, cmdArgs)
	conf, err := renderTemplate("upstart", upstartTemplate, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(upstartConfPath, []byte(conf), 0o644); err != nil {
		return fmt.Errorf("failed to write upstart config: %w", err)
	}
	log.Info().Msgf("wrote upstart config: %s", upstartConfPath)

	if out, err := exec.Command("initctl", "reload-configuration").CombinedOutput(); err != nil {
		return fmt.Errorf("initctl reload-configuration: %w (output: %s)", err, string(out))
	}
	if out, err := exec.Command("initctl", "start", config.AppName).CombinedOutput(); err != nil {
		return fmt.Errorf("initctl start: %w (output: %s)", err, string(out))
	}
	log.Info().Msg("KubeSolo service installed and started via upstart")
	return nil
}

func (m *upstartManager) Uninstall() error {
	_ = exec.Command("initctl", "stop", config.AppName).Run()
	_ = os.Remove(upstartConfPath)
	_ = exec.Command("initctl", "reload-configuration").Run()
	log.Info().Msg("KubeSolo upstart service removed")
	return nil
}
