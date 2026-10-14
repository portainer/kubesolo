package cli

import (
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/cli/config"
)

func containerConfig() config.Config {
	return config.Config{
		Name:    config.AppName,
		Version: config.DefaultVersion,
		Path:    config.DefaultPath,
		RunMode: config.RunModeContainer,
	}
}

func TestCheckContainerSettings(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*config.Config)
		wantErr string
	}{
		{"defaults", func(*config.Config) {}, ""},
		{"valid settings", func(c *config.Config) { c.MTU = "1400"; c.D2K = true; c.D2KNamespace = "d2k" }, ""},
		{"path outside the volume", func(c *config.Config) { c.Path = "/data/ks" }, "--path"},
		{"offline install", func(c *config.Config) { c.OfflineInstall = "/tmp/kubesolo.tar.gz" }, "--offline-install"},
		{"invalid MTU", func(c *config.Config) { c.MTU = "abc" }, "abc"},
		{"invalid system reserved", func(c *config.Config) { c.SystemReserved = "bogus" }, "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := containerConfig()
			tc.edit(&cfg)
			_, err := checkContainerSettings(&cfg)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}
