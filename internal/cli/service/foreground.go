package service

import (
	"os"

	"github.com/portainer/kubesolo/internal/cli/config"
)

type foregroundManager struct{}

func (m *foregroundManager) Uninstall() error {
	// Foreground mode has no persistent service to remove
	return nil
}

// buildForegroundEnv returns the process environment with optional proxy vars injected.
func buildForegroundEnv(cfg *config.Config) []string {
	env := os.Environ()
	if cfg.Proxy != "" {
		env = append(env,
			"HTTP_PROXY="+cfg.Proxy,
			"HTTPS_PROXY="+cfg.Proxy,
			"NO_PROXY=localhost,127.0.0.1",
		)
	}
	return env
}
