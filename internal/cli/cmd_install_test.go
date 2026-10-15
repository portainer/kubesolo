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

func TestContainerSettings(t *testing.T) {
	cases := []struct {
		name        string
		edit        func(*config.Config)
		wantErr     string
		wantDropped string
		wantFlags   bool
	}{
		{name: "defaults", edit: func(*config.Config) {}},
		{name: "valid settings", edit: func(c *config.Config) { c.D2K = true; c.D2KNamespace = "d2k"; c.Debug = true }},
		{name: "irrelevant settings are dropped", edit: func(c *config.Config) { c.Path = "/data/ks"; c.MTU = "1400"; c.CPUManagerPolicy = "static" }, wantDropped: "--path --mtu --cpu-manager-policy"},
		{name: "offline install", edit: func(c *config.Config) { c.OfflineInstall = "/tmp/kubesolo.tar.gz" }, wantErr: "--offline-install"},
		{name: "invalid system reserved", edit: func(c *config.Config) { c.SystemReserved = "bogus" }, wantErr: "bogus"},
		{name: "release before the configuration file", edit: func(c *config.Config) { c.Version = "v1.2.0"; c.MTU = "1400" }, wantDropped: "--mtu", wantFlags: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := containerConfig()
			tc.edit(&cfg)
			args, doc, dropped, _, err := containerSettings(&cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want one mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := strings.Join(dropped, " "); got != tc.wantDropped {
				t.Errorf("dropped = %q, want %q", got, tc.wantDropped)
			}
			if tc.wantFlags {
				// Every flag was dropped, so the image's own command line applies.
				if doc != nil || len(args) != 0 {
					t.Errorf("args = %v, doc = %s; want no configuration file and no flags", args, doc)
				}
				return
			}
			if strings.Join(args, " ") != "--config=/etc/kubesolo/config.yaml" || doc == nil {
				t.Errorf("args = %v, doc = %s", args, doc)
			}
			if strings.Contains(string(doc), "mtu: 1400") {
				t.Errorf("an ignored setting reached the file:\n%s", doc)
			}
		})
	}
}
