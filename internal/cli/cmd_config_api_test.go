package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/pkg/components/configapi"
	"github.com/portainer/kubesolo/types"
)

// withRunningAPI starts a configuration API against a config file that points at
// its own socket, so resolveBackend discovers it the same way it would in
// production. Returns the config file path.
func withRunningAPI(t *testing.T, seed func(*types.Config)) string {
	t.Helper()

	// Short directory: unix socket paths are length-limited, and t.TempDir()
	// embeds the test name.
	dir, err := os.MkdirTemp("", "ks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	configPath := filepath.Join(dir, "config.yaml")
	socketPath := filepath.Join(dir, "c.sock")

	cfg := kubesoloconfig.Defaults()
	cfg.API.Enabled = true
	cfg.API.SocketPath = socketPath
	if seed != nil {
		seed(cfg)
	}
	if err := kubesoloconfig.Write(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	svc := configapi.NewService(ctx, cancel, ready, configapi.Options{
		SocketPath: socketPath,
		ConfigPath: configPath,
		Host:       kubesoloconfig.Host{NumCPU: 4, GOARCH: "amd64"},
	})

	done := make(chan error, 1)
	go func() { done <- svc.Run() }()
	<-ready
	t.Cleanup(func() { cancel(); <-done })

	return configPath
}

// TestSetGoesThroughTheAPIWhenRunning is the point of the task: with KubeSolo
// running, the change goes through its API rather than editing the file
// underneath it.
func TestSetGoesThroughTheAPIWhenRunning(t *testing.T) {
	configPath := withRunningAPI(t, nil)

	if _, err := runConfig(t, "set", "network.mtu", "1400", "--config", configPath); err != nil {
		t.Fatal(err)
	}

	// The API wrote the file, so the value is durable either way — what matters
	// is that it went through validation and reporting on the way.
	out, err := runConfig(t, "get", "network.mtu", "--config", configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "1400" {
		t.Errorf("got %q, want 1400", strings.TrimSpace(out))
	}
}

// TestSetThroughAPIPreservesOtherSettings covers why `set` sends a merge patch
// for one setting rather than the whole document: a change made by anything else
// in between must survive.
func TestSetThroughAPIPreservesOtherSettings(t *testing.T) {
	configPath := withRunningAPI(t, func(c *types.Config) {
		c.Network.NodeIP = "10.0.0.5"
		c.D2K.Namespace = "workloads"
	})

	if _, err := runConfig(t, "set", "network.mtu", "1400", "--config", configPath); err != nil {
		t.Fatal(err)
	}

	stored := kubesoloconfig.Defaults()
	if _, _, err := kubesoloconfig.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Network.NodeIP != "10.0.0.5" || stored.D2K.Namespace != "workloads" {
		t.Errorf("unrelated settings were lost: %+v", stored)
	}
	if stored.Network.MTU != 1400 {
		t.Errorf("mtu = %d", stored.Network.MTU)
	}
}

// TestSetTypedValuesThroughAPI checks that the patch carries the right JSON
// type. A list sent as a string, or an integer sent as a string, would be
// rejected or silently mangled.
func TestSetTypedValuesThroughAPI(t *testing.T) {
	for _, tc := range []struct{ setting, value, want string }{
		{"logging.debug", "true", "true"},
		{"network.mtu", "1400", "1400"},
		{"kubernetes.apiServer.extraSANs", "a.local,b.local", "- a.local\n- b.local"},
		{"kubernetes.kubelet.systemReserved", "cpu=1,memory=500Mi", "cpu: \"1\"\nmemory: 500Mi"},
		{"d2k.namespace", "workloads", "workloads"},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			configPath := withRunningAPI(t, nil)

			if _, err := runConfig(t, "set", tc.setting, tc.value, "--config", configPath); err != nil {
				t.Fatal(err)
			}
			out, err := runConfig(t, "get", tc.setting, "--config", configPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out) != tc.want {
				t.Errorf("got %q, want %q", strings.TrimSpace(out), tc.want)
			}
		})
	}
}

// TestSetImmutableThroughAPIIsRefused — via the API this is a hard refusal, not
// the warning a direct file edit gets, because a running install has state below
// that path.
func TestSetImmutableThroughAPIIsRefused(t *testing.T) {
	configPath := withRunningAPI(t, nil)

	_, err := runConfig(t, "set", "path", "/somewhere/else", "--config", configPath)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "path") {
		t.Errorf("error should name the setting, got: %v", err)
	}

	stored := kubesoloconfig.Defaults()
	if _, _, err := kubesoloconfig.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Path != types.DefaultBasePath {
		t.Errorf("path was changed to %q", stored.Path)
	}
}

// TestInvalidSetThroughAPILeavesFileUntouched — the same guarantee as the file
// path, but enforced by the server.
func TestInvalidSetThroughAPILeavesFileUntouched(t *testing.T) {
	configPath := withRunningAPI(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.1" })

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runConfig(t, "set", "kubernetes.kubelet.cpuManager.policy", "dynamic", "--config", configPath); err == nil {
		t.Fatal("expected a refusal")
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a rejected set modified the configuration file")
	}
}

// TestGetThroughAPIRedactsSecrets — reading does not need the credential, so it
// is not fetched.
func TestGetThroughAPIRedactsSecrets(t *testing.T) {
	const secret = "real-edge-key"
	configPath := withRunningAPI(t, func(c *types.Config) { c.Portainer.EdgeKey = secret })

	out, err := runConfig(t, "get", "--config", configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, secret) {
		t.Error("the edge key was printed")
	}
}

// TestSetPreservesSecretsThroughAPI is the read-modify-write hazard from the
// client side: `set` must fetch the real credential, or the patch would carry
// the redaction placeholder and be refused.
func TestSetPreservesSecretsThroughAPI(t *testing.T) {
	const secret = "real-edge-key"
	configPath := withRunningAPI(t, func(c *types.Config) { c.Portainer.EdgeKey = secret })

	if _, err := runConfig(t, "set", "network.mtu", "1400", "--config", configPath); err != nil {
		t.Fatalf("set must not be blocked by the redaction guard: %v", err)
	}

	stored := kubesoloconfig.Defaults()
	if _, _, err := kubesoloconfig.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Portainer.EdgeKey != secret {
		t.Errorf("the credential was damaged: %q", stored.Portainer.EdgeKey)
	}
}

// TestValidateThroughAPI uses the running instance's own host information.
func TestValidateThroughAPI(t *testing.T) {
	t.Run("accepts a valid configuration", func(t *testing.T) {
		configPath := withRunningAPI(t, nil)
		if _, err := runConfig(t, "validate", "--config", configPath); err != nil {
			t.Fatalf("a valid configuration must pass: %v", err)
		}
	})

	t.Run("-f still checks the named file locally", func(t *testing.T) {
		configPath := withRunningAPI(t, nil)
		bad := seed(t, func(c *types.Config) {
			c.D2K.Enabled = true
			c.Network.LoadBalancer.Enabled = false
		})
		if _, err := runConfig(t, "validate", "--config", configPath, "-f", bad); err == nil {
			t.Fatal("-f must check the named file, not the running instance")
		}
	})
}

// TestFallsBackToTheFileWhenNothingIsListening covers the common case — KubeSolo
// stopped, or the API disabled — and the stale-socket case, where the file
// remains after an unclean shutdown.
func TestFallsBackToTheFileWhenNothingIsListening(t *testing.T) {
	dir, err := os.MkdirTemp("", "ks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	configPath := filepath.Join(dir, "config.yaml")
	socketPath := filepath.Join(dir, "c.sock")

	cfg := kubesoloconfig.Defaults()
	cfg.API.Enabled = true
	cfg.API.SocketPath = socketPath
	if err := kubesoloconfig.Write(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	// A socket file with nothing behind it, as SIGKILL would leave.
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runConfig(t, "set", "network.mtu", "1400", "--config", configPath); err != nil {
		t.Fatalf("must fall back to the file: %v", err)
	}

	stored := kubesoloconfig.Defaults()
	if _, _, err := kubesoloconfig.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Network.MTU != 1400 {
		t.Errorf("mtu = %d; the file was not written", stored.Network.MTU)
	}
}

// TestEditThroughAPI covers the whole-document write path.
func TestEditThroughAPI(t *testing.T) {
	configPath := withRunningAPI(t, nil)
	scriptedEditor(t, "s|^  nodeIP: .*|  nodeIP: 10.9.9.9|")

	if _, err := runConfig(t, "edit", "--config", configPath); err != nil {
		t.Fatal(err)
	}

	stored := kubesoloconfig.Defaults()
	if _, _, err := kubesoloconfig.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Network.NodeIP != "10.9.9.9" {
		t.Errorf("nodeIP = %q", stored.Network.NodeIP)
	}
}

// TestEditThroughAPIPreservesSecrets is why edit reads unredacted: a whole
// document write carrying "***" would be refused, and one carrying an empty
// value would destroy the credential.
func TestEditThroughAPIPreservesSecrets(t *testing.T) {
	const secret = "real-edge-key"
	configPath := withRunningAPI(t, func(c *types.Config) { c.Portainer.EdgeKey = secret })
	scriptedEditor(t, "s|^  mtu: .*|  mtu: 1400|")

	if _, err := runConfig(t, "edit", "--config", configPath); err != nil {
		t.Fatal(err)
	}

	stored := kubesoloconfig.Defaults()
	if _, _, err := kubesoloconfig.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Portainer.EdgeKey != secret {
		t.Errorf("the credential was lost through edit: %q", stored.Portainer.EdgeKey)
	}
	if stored.Network.MTU != 1400 {
		t.Errorf("the edit did not apply, mtu = %d", stored.Network.MTU)
	}
}
