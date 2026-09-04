package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/portainer/kubesolo/types"
	"sigs.k8s.io/yaml"
)

// TestDefaultsRoundTrip is the property that makes the config file safe to
// rewrite: serialising a config and reading it back must not change it. If it
// does, the API would silently mutate settings it was only asked to display.
func TestDefaultsRoundTrip(t *testing.T) {
	want := Defaults()

	raw, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got types.Config
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(*want, got) {
		t.Errorf("round trip changed the config\n got: %+v\nwant: %+v", got, *want)
	}
}

// TestContainerModeTriState guards the one field where the zero value and
// "unset" mean different things: nil is auto-detect, false is an explicit
// override. A plain bool would collapse the two.
func TestContainerModeTriState(t *testing.T) {
	no, yes := false, true
	for _, tc := range []struct {
		name string
		yaml string
		want *bool
	}{
		{"absent means auto-detect", "runtime: {}", nil},
		{"explicit false", "runtime:\n  containerMode: false\n", &no},
		{"explicit true", "runtime:\n  containerMode: true\n", &yes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg types.Config
			if err := yaml.Unmarshal([]byte(tc.yaml), &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := cfg.Runtime.ContainerMode
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("got %v, want nil", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("got nil, want %v", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Fatalf("got %v, want %v", *got, *tc.want)
			}
		})
	}
}

// TestPartialConfigKeepsDefaults pins the merge behaviour the config file and
// the API's PATCH both rely on: unmarshalling a partial document over a
// populated struct leaves absent keys alone, so a user who sets one field does
// not silently reset the rest.
func TestPartialConfigKeepsDefaults(t *testing.T) {
	cfg := Defaults()

	if err := yaml.Unmarshal([]byte("network:\n  nodeIP: 10.0.0.5\n"), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Network.NodeIP != "10.0.0.5" {
		t.Errorf("nodeIP = %q, want 10.0.0.5", cfg.Network.NodeIP)
	}
	if !cfg.Network.LoadBalancer.Enabled {
		t.Error("loadBalancer.enabled was reset to false by an unrelated key")
	}
	if cfg.Portainer.Image != types.DefaultPortainerEdgeImage {
		t.Errorf("portainer.image = %q, want the default", cfg.Portainer.Image)
	}
}

// TestDefaultDocumentGolden pins the on-disk shape of the default config. The
// golden file is the reference document the docs quote, so a schema change that
// is not intended shows up here first.
func TestDefaultDocumentGolden(t *testing.T) {
	raw, err := yaml.Marshal(Defaults())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	path := filepath.Join("testdata", "default-config.yaml")
	if *update {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if string(raw) != string(want) {
		t.Errorf("default config document changed\n--- got ---\n%s\n--- want ---\n%s", raw, want)
	}
}
