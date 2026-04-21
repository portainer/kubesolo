package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/rs/zerolog/log"
)

const runitServiceDir = "/etc/runit/sv/kubesolo"

const runitRunTemplate = `#!/bin/sh
{{- if .Proxy}}
export HTTP_PROXY={{.Proxy | shellQuote}}
export HTTPS_PROXY={{.Proxy | shellQuote}}
export NO_PROXY='localhost,127.0.0.1'
{{- end}}
exec {{.InstallPath}}{{range .CmdArgsList}} {{. | shellQuote}}{{end}}
`

type runitManager struct{}

func (m *runitManager) Install(cfg *config.Config, cmdArgs []string) error {
	if err := os.MkdirAll(runitServiceDir, 0o755); err != nil {
		return fmt.Errorf("failed to create runit service directory: %w", err)
	}

	data := buildTemplateData(cfg, cmdArgs)
	runScript, err := renderTemplate("runit-run", runitRunTemplate, data)
	if err != nil {
		return err
	}
	runPath := filepath.Join(runitServiceDir, "run")
	if err := os.WriteFile(runPath, []byte(runScript), 0o755); err != nil {
		return fmt.Errorf("failed to write runit run script: %w", err)
	}

	// Enable by symlinking into the active service directory.
	// Not all runit layouts use /var/service or /etc/runit/runsvdir/default; if
	// neither directory exists the service must be enabled manually, so a missing
	// directory is a warning rather than a hard failure.
	for _, svcDir := range []string{"/var/service", "/etc/runit/runsvdir/default"} {
		if _, err := os.Stat(svcDir); err == nil {
			link := filepath.Join(svcDir, config.AppName)
			_ = os.Remove(link)
			if err := os.Symlink(runitServiceDir, link); err != nil {
				log.Warn().Err(err).Msgf("could not symlink runit service into %s", svcDir)
			} else {
				log.Info().Msgf("runit service enabled in %s", svcDir)
			}
			break
		}
	}

	log.Info().Msgf("wrote runit service directory: %s", runitServiceDir)
	log.Info().Msg("KubeSolo service installed via runit")
	return nil
}

func (m *runitManager) Uninstall() error {
	for _, svcDir := range []string{"/var/service", "/etc/runit/runsvdir/default"} {
		_ = os.Remove(filepath.Join(svcDir, config.AppName))
	}
	_ = os.RemoveAll(runitServiceDir)
	log.Info().Msg("KubeSolo runit service removed")
	return nil
}
