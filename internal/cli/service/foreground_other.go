//go:build !linux

package service

import (
	"os"
	"os/exec"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

// Install runs KubeSolo attached to the current TTY. On non-Linux platforms
// syscall.Exec is not available, so we fork a child and wait — this is only
// used on dev machines; production targets are always Linux.
func (m *foregroundManager) Install(cfg *config.Config, cmdArgs []string) error {
	log.Info().Msgf("launching KubeSolo in foreground: %s %s", config.DefaultInstallPath, strings.Join(cmdArgs, " "))
	log.Info().Msg("press Ctrl+C to stop")

	cmd := exec.Command(config.DefaultInstallPath, cmdArgs...)
	cmd.Env = buildForegroundEnv(cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
