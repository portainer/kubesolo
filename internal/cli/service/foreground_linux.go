package service

import (
	"strings"
	"syscall"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

// Install replaces the current process with the KubeSolo binary via execve,
// matching the bash script's `eval exec ...` pattern. The installer process
// becomes KubeSolo, so signals and exit codes flow through naturally — this
// is especially important in containers where the installer may be PID 1.
func (m *foregroundManager) Install(cfg *config.Config, cmdArgs []string) error {
	log.Info().Msgf("launching KubeSolo in foreground: %s %s", config.DefaultInstallPath, strings.Join(cmdArgs, " "))
	log.Info().Msg("press Ctrl+C to stop")

	argv := append([]string{config.DefaultInstallPath}, cmdArgs...)
	return syscall.Exec(config.DefaultInstallPath, argv, buildForegroundEnv(cfg))
}
