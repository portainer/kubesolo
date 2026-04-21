package service

import (
	"os"
	"os/exec"
	"strings"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/rs/zerolog/log"
)

type foregroundManager struct{}

// Install for foreground mode replaces the current process with the KubeSolo
// binary via exec (like the bash script's `eval exec ...`). This means the
// installer process becomes KubeSolo — signals and exit codes flow naturally.
func (m *foregroundManager) Install(cfg *config.Config, cmdArgs []string) error {
	log.Info().Msgf("launching KubeSolo in foreground: %s %s", config.DefaultInstallPath, strings.Join(cmdArgs, " "))
	log.Info().Msg("press Ctrl+C to stop")

	env := os.Environ()
	if cfg.Proxy != "" {
		env = append(env,
			"HTTP_PROXY="+cfg.Proxy,
			"HTTPS_PROXY="+cfg.Proxy,
			"NO_PROXY=localhost,127.0.0.1",
		)
	}

	cmd := exec.Command(config.DefaultInstallPath, cmdArgs...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (m *foregroundManager) Uninstall() error {
	// Foreground mode has no persistent service to remove
	return nil
}
