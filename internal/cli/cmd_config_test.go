package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/types"
	"sigs.k8s.io/yaml"
)

// runConfig executes `kubesoloctl config ...` and returns what it wrote to
// stdout. Diagnostics go to stderr and are not captured.
func runConfig(t *testing.T, args ...string) (string, error) {
	t.Helper()

	real := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	cmd := configCmd()
	cmd.SetArgs(args)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	runErr := cmd.Execute()

	os.Stdout = real
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return out.String(), runErr
}

// seed writes a starting configuration and returns its path.
func seed(t *testing.T, adjust func(*types.Config)) string {
	t.Helper()
	cfg := kubesoloconfig.Defaults()
	if adjust != nil {
		adjust(cfg)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := kubesoloconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigGetWholeDocument(t *testing.T) {
	path := seed(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.5" })

	out, err := runConfig(t, "get", "--config", path)
	if err != nil {
		t.Fatal(err)
	}

	var got types.Config
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not a config document: %v\n%s", err, out)
	}
	if got.Network.NodeIP != "10.0.0.5" {
		t.Errorf("nodeIP = %q", got.Network.NodeIP)
	}
}

func TestConfigGetSingleSetting(t *testing.T) {
	path := seed(t, func(c *types.Config) {
		c.Network.NodeIP = "10.0.0.5"
		c.Kubernetes.APIServer.ExtraSANs = []string{"a.local", "b.local"}
	})

	for _, tc := range []struct{ path, want string }{
		{"network.nodeIP", "10.0.0.5"},
		{"d2k.namespace", "d2k"},
		{"logging.debug", "false"},
		{"kubernetes.apiServer.startupTimeoutSeconds", "600"},
		{"kubernetes.apiServer.extraSANs", "- a.local\n- b.local"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			out, err := runConfig(t, "get", tc.path, "--config", path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out) != tc.want {
				t.Errorf("got %q, want %q", strings.TrimSpace(out), tc.want)
			}
		})
	}
}

func TestConfigGetUnknownPathSuggests(t *testing.T) {
	path := seed(t, nil)

	_, err := runConfig(t, "get", "network.nodeIp", "--config", path)
	if err == nil {
		t.Fatal("expected an error for an unknown setting")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error should suggest near matches, got: %v", err)
	}
}

func TestConfigSetRoundTrips(t *testing.T) {
	path := seed(t, nil)

	if _, err := runConfig(t, "set", "network.nodeIP", "10.0.0.9", "--config", path); err != nil {
		t.Fatal(err)
	}

	out, err := runConfig(t, "get", "network.nodeIP", "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "10.0.0.9" {
		t.Errorf("got %q, want 10.0.0.9", strings.TrimSpace(out))
	}
}

func TestConfigSetTypedValues(t *testing.T) {
	path := seed(t, nil)

	for _, tc := range []struct{ setting, value, want string }{
		{"logging.debug", "true", "true"},
		{"network.mtu", "1400", "1400"},
		{"kubernetes.apiServer.extraSANs", "a.local,b.local", "- a.local\n- b.local"},
		{"kubernetes.kubelet.systemReserved", "cpu=1,memory=500Mi", "cpu: \"1\"\nmemory: 500Mi"},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			if _, err := runConfig(t, "set", tc.setting, tc.value, "--config", path); err != nil {
				t.Fatal(err)
			}
			out, err := runConfig(t, "get", tc.setting, "--config", path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out) != tc.want {
				t.Errorf("got %q, want %q", strings.TrimSpace(out), tc.want)
			}
		})
	}
}

// TestConfigSetRejectedLeavesFileUntouched is the property that makes `set` safe
// to run against a live install: a value that fails validation must not damage
// the configuration already in place.
func TestConfigSetRejectedLeavesFileUntouched(t *testing.T) {
	for _, tc := range []struct{ name, setting, value string }{
		{"unparseable type", "network.mtu", "not-a-number"},
		{"unknown cpu policy", "kubernetes.kubelet.cpuManager.policy", "dynamic"},
		{"unsupported reserved resource", "kubernetes.kubelet.systemReserved", "gpu=1"},
		{"bad image reference", "portainer.image", "NOT A REF"},
		{"relative runtime endpoint", "runtime.endpoint", "run/crio.sock"},
		{"unknown setting", "network.nope", "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := seed(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.1" })
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := runConfig(t, "set", tc.setting, tc.value, "--config", path); err == nil {
				t.Fatal("expected the command to fail")
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("the configuration file was modified by a rejected set:\n--- before ---\n%s\n--- after ---\n%s", before, after)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Run("accepts a valid file", func(t *testing.T) {
		path := seed(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.5" })
		if _, err := runConfig(t, "validate", "--config", path); err != nil {
			t.Fatalf("a valid configuration must pass: %v", err)
		}
	})

	t.Run("rejects an invalid file", func(t *testing.T) {
		path := seed(t, func(c *types.Config) {
			c.D2K.Enabled = true
			c.Network.LoadBalancer.Enabled = false
		})
		if _, err := runConfig(t, "validate", "--config", path); err == nil {
			t.Fatal("expected d2k without the load balancer to be rejected")
		}
	})

	t.Run("-f overrides the installed file", func(t *testing.T) {
		good := seed(t, nil)
		bad := seed(t, func(c *types.Config) {
			c.D2K.Enabled = true
			c.Network.LoadBalancer.Enabled = false
		})
		if _, err := runConfig(t, "validate", "--config", good, "-f", bad); err == nil {
			t.Fatal("-f must select the file that is checked")
		}
	})
}

func TestConfigSchemaCoversEverySetting(t *testing.T) {
	out, err := runConfig(t, "schema")
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		APIVersion string                      `json:"apiVersion"`
		Settings   []kubesoloconfig.Descriptor `json:"settings"`
	}
	// YAML, like every other kubesoloctl output. sigs.k8s.io/yaml honours the
	// json tags, so one set of tags serves the CLI and the HTTP API both.
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("schema is not valid YAML: %v\n%s", err, out)
	}
	if doc.APIVersion != types.ConfigAPIVersion {
		t.Errorf("apiVersion = %q", doc.APIVersion)
	}

	byPath := map[string]kubesoloconfig.Descriptor{}
	for _, d := range doc.Settings {
		byPath[d.Path] = d
	}
	for _, f := range kubesoloconfig.Registry() {
		if _, ok := byPath[f.ConfigPath]; !ok {
			t.Errorf("schema omits %s", f.ConfigPath)
		}
	}

	// The properties a user interface actually depends on.
	if got := byPath["portainer.edgeKey"]; !got.Secret || got.Default != nil {
		t.Errorf("edgeKey must be marked secret with no default, got %+v", got)
	}
	if got := byPath["path"]; got.Mutability != "immutable" {
		t.Errorf("path mutability = %q, want immutable", got.Mutability)
	}
	if got := byPath["runtime.containerMode"]; got.Type != "boolean" || got.Default != nil {
		t.Errorf("containerMode must be a boolean with no default (tri-state), got %+v", got)
	}
	if got := byPath["kubernetes.apiServer.extraSANs"]; got.Type != "array" {
		t.Errorf("extraSANs type = %q, want array", got.Type)
	}
}

// scriptedEditor installs a fake $EDITOR that applies sed to the file it is
// given, so `config edit` can be exercised without a terminal.
func scriptedEditor(t *testing.T, sedExpr string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "editor")
	body := "#!/bin/sh\nsed -i.bak '" + sedExpr + "' \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script)
}

func TestConfigEditSavesValidChanges(t *testing.T) {
	path := seed(t, nil)
	scriptedEditor(t, "s|^  nodeIP: .*|  nodeIP: 10.9.9.9|")

	if _, err := runConfig(t, "edit", "--config", path); err != nil {
		t.Fatal(err)
	}

	out, err := runConfig(t, "get", "network.nodeIP", "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "10.9.9.9" {
		t.Errorf("got %q, want 10.9.9.9", strings.TrimSpace(out))
	}
}

// TestConfigEditInvalidValueLeavesFileUntouched covers the case that parses
// cleanly but fails validation, which reaches further into runConfigEdit than a
// malformed document does. Without it, moving the write ahead of validation goes
// unnoticed.
func TestConfigEditInvalidValueLeavesFileUntouched(t *testing.T) {
	// d2k is on and valid to start with; the edit disables the load balancer it
	// depends on.
	path := seed(t, func(c *types.Config) { c.D2K.Enabled = true })
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	scriptedEditor(t, "s|^    enabled: true|    enabled: false|")

	if _, err := runConfig(t, "edit", "--config", path); err == nil {
		t.Fatal("expected the edit to be rejected by validation")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("an edit rejected by validation modified the installed configuration")
	}
}

// TestConfigEditRejectedLeavesFileUntouched is the data-loss guard: edits that
// fail validation must not reach the installed file, and must not be discarded
// either.
func TestConfigEditRejectedLeavesFileUntouched(t *testing.T) {
	path := seed(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.1" })
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	scriptedEditor(t, "s|^  mtu: .*|  mtu: not-a-number|")

	_, runErr := runConfig(t, "edit", "--config", path)
	if runErr == nil {
		t.Fatal("expected the edit to be rejected")
	}
	if !strings.Contains(runErr.Error(), "your edits are kept at") {
		t.Errorf("the user's work must not be silently discarded, got: %v", runErr)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a rejected edit modified the installed configuration")
	}
}
