package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

const s6ServiceDir = "/etc/s6/sv/kubesolo"

const s6RunTemplate = `#!/bin/sh
{{- if .Proxy}}
export HTTP_PROXY={{.Proxy | shellQuote}}
export HTTPS_PROXY={{.Proxy | shellQuote}}
export NO_PROXY='localhost,127.0.0.1'
{{- end}}
exec {{.InstallPath}}{{range .CmdArgsList}} {{. | shellQuote}}{{end}}
`

const s6FinishScript = `#!/bin/sh
echo "kubesolo service finished"
`

type s6Manager struct{}

func (m *s6Manager) Install(cfg *config.Config, cmdArgs []string) error {
	if err := os.MkdirAll(s6ServiceDir, 0o755); err != nil {
		return fmt.Errorf("failed to create s6 service directory: %w", err)
	}

	data := buildTemplateData(cfg, cmdArgs)
	runScript, err := renderTemplate("s6-run", s6RunTemplate, data)
	if err != nil {
		return err
	}
	runPath := filepath.Join(s6ServiceDir, "run")
	if err := os.WriteFile(runPath, []byte(runScript), 0o755); err != nil {
		return fmt.Errorf("failed to write s6 run script: %w", err)
	}

	finishPath := filepath.Join(s6ServiceDir, "finish")
	if err := os.WriteFile(finishPath, []byte(s6FinishScript), 0o755); err != nil {
		return fmt.Errorf("failed to write s6 finish script: %w", err)
	}

	// Enable: symlink into the admin supervision directory if it exists
	for _, scanDir := range []string{"/etc/s6/adminsv/default", "/etc/s6-overlay/s6-rc.d"} {
		if _, err := os.Stat(scanDir); err == nil {
			link := filepath.Join(scanDir, config.AppName)
			_ = os.Remove(link)
			if err := os.Symlink(s6ServiceDir, link); err != nil {
				log.Warn().Err(err).Msgf("could not symlink s6 service into %s", scanDir)
			}
			break
		}
	}

	// Start if s6-svc is available
	if _, err := exec.LookPath("s6-svc"); err == nil {
		if out, err := exec.Command("s6-svc", "-u", s6ServiceDir).CombinedOutput(); err != nil {
			log.Warn().Msgf("s6-svc -u returned error (supervisor may not be running yet): %s", string(out))
		}
	}

	log.Info().Msgf("wrote s6 service directory: %s", s6ServiceDir)
	log.Info().Msg("KubeSolo service installed via s6")
	return nil
}

func (m *s6Manager) Uninstall() error {
	if _, err := exec.LookPath("s6-svc"); err == nil {
		_ = exec.Command("s6-svc", "-d", s6ServiceDir).Run()
	}
	for _, scanDir := range []string{"/etc/s6/adminsv/default", "/etc/s6-overlay/s6-rc.d"} {
		_ = os.Remove(filepath.Join(scanDir, config.AppName))
	}
	_ = os.RemoveAll(s6ServiceDir)
	log.Info().Msg("KubeSolo s6 service removed")
	return nil
}
