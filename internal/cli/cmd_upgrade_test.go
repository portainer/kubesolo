package cli

import (
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/cli/config"
)

func TestContainerUpgradeImage(t *testing.T) {
	cases := []struct {
		current, explicit, want, wantErr string
	}{
		{current: "portainer/kubesolo:v1.2.1", want: "portainer/kubesolo:v1.2.2"},
		{current: "docker.io/portainer/kubesolo:v1.2.1", want: "portainer/kubesolo:v1.2.2"},
		{current: "localhost:5099/kubesolo:custom", wantErr: "--image"},
		{current: "portainerci/kubesolo:pr-42", wantErr: "--image"},
		{current: "localhost:5099/kubesolo:custom", explicit: "localhost:5099/kubesolo:next", want: "localhost:5099/kubesolo:next"},
	}
	for _, c := range cases {
		got, err := containerUpgradeImage(c.current, c.explicit, "v1.2.2")
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want one mentioning %s", c.current, err, c.wantErr)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.current, got, err, c.want)
		}
	}
}

func TestCheckContainerVersion(t *testing.T) {
	if err := checkContainerVersion("v1.2.1", "v1.2.2", false); err != nil {
		t.Errorf("upgrade: %v", err)
	}
	for _, target := range []string{"v1.2.2", "v1.2.1"} {
		err := checkContainerVersion("v1.2.2", target, false)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Errorf("%s over v1.2.2: err = %v, want a --force hint", target, err)
		}
		if err := checkContainerVersion("v1.2.2", target, true); err != nil {
			t.Errorf("%s with --force: %v", target, err)
		}
	}
}

func TestContainerUpgradeArgs(t *testing.T) {
	flags := []string{"--path=/var/lib/kubesolo", "--mtu=1400", "--d2k", "--d2k-namespace=d2k", "--debug"}

	args, doc, dropped, err := containerUpgradeArgs(flags, &config.Config{Version: "v1.2.2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, " ") != "--config=/etc/kubesolo/config.yaml" || doc == nil {
		t.Fatalf("args = %v, doc = %s", args, doc)
	}
	if strings.Join(dropped, " ") != "--mtu" {
		t.Errorf("dropped = %v", dropped)
	}
	if !strings.Contains(string(doc), "debug: true") || strings.Contains(string(doc), "mtu: 1400") {
		t.Errorf("doc:\n%s", doc)
	}

	// Already on a configuration file: kept as it is.
	onFile := []string{"--config=/etc/kubesolo/config.yaml"}
	if args, doc, _, err := containerUpgradeArgs(onFile, &config.Config{Version: "v1.2.3"}); err != nil || doc != nil || strings.Join(args, " ") != onFile[0] {
		t.Errorf("config file: args = %v, doc = %s, err = %v", args, doc, err)
	}

	// A target that predates the configuration file keeps the flags.
	if args, doc, _, err := containerUpgradeArgs(flags, &config.Config{Version: "v1.2.0"}); err != nil || doc != nil || len(args) != len(flags) {
		t.Errorf("old target: args = %v, doc = %s, err = %v", args, doc, err)
	}
}

// install and upgrade register flags against the same config. Registering a
// flag stores its default, so upgrade must not register one over a field whose
// default install takes from the environment.
func TestUpgradeFlagsKeepInstallEnvironmentDefaults(t *testing.T) {
	t.Setenv("KUBESOLO_IMAGE", "registry.example.com/kubesolo:dev")
	var cfg config.Config
	installCmd(&cfg)
	upgradeCmd(&cfg)
	if cfg.ContainerImage != "registry.example.com/kubesolo:dev" {
		t.Errorf("KUBESOLO_IMAGE lost: ContainerImage = %q", cfg.ContainerImage)
	}
}
