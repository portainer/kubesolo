package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/types"
	"sigs.k8s.io/yaml"
)

// samples returns two distinct values for a field, in the string form a flag or
// environment variable would carry. The losing layers of each precedence case
// get lo and the winning layer gets hi, so seeing lo means the wrong layer won.
// Two values suffice even for booleans, which have no third.
func samples(f Field) (lo, hi string) {
	switch f.Get(Defaults()).(type) {
	case bool, *bool:
		return "false", "true"
	case int:
		return "1", "2"
	case []string:
		return "lo.example", "hi.example"
	case map[string]string:
		return "k=lo", "k=hi"
	default:
		return "lo", "hi"
	}
}

// want returns what Get should report once value has been applied to a field.
func want(f Field, value string) any {
	cfg := Defaults()
	if err := f.Set(cfg, value); err != nil {
		panic(err)
	}
	return f.Get(cfg)
}

// writeConfig renders a config file that sets exactly one field.
func writeConfig(t *testing.T, f Field, value string) string {
	t.Helper()
	cfg := Defaults()
	if err := f.Set(cfg, value); err != nil {
		t.Fatalf("set %s: %v", f.ConfigPath, err)
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeFile writes a literal config file for a test.
func writeFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

// clearEnv removes every KUBESOLO_ variable for the duration of the test, so an
// operator's own environment cannot make the suite pass or fail.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, f := range Registry() {
		if f.Envar == "" {
			continue
		}
		if old, ok := os.LookupEnv(f.Envar); ok {
			if err := os.Unsetenv(f.Envar); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Setenv(f.Envar, old) })
		}
	}
}

// TestPrecedence is the property the whole layered configuration rests on:
// command line beats environment beats file beats default, for every setting.
//
// It is driven off Registry(), so a setting added without precedence coverage
// is impossible rather than merely unlikely.
func TestPrecedence(t *testing.T) {
	cases := []struct {
		name            string
		file, env, flag bool
		winner          string // "file", "env", "flag" or "" for the default
	}{
		{name: "default only", winner: ""},
		{name: "file over default", file: true, winner: "file"},
		{name: "env over file", file: true, env: true, winner: "env"},
		{name: "flag over env and file", file: true, env: true, flag: true, winner: "flag"},
		{name: "flag over env", env: true, flag: true, winner: "flag"},
		{name: "flag over file", file: true, flag: true, winner: "flag"},
		{name: "env over default", env: true, winner: "env"},
	}

	for _, f := range Registry() {
		t.Run(f.ConfigPath, func(t *testing.T) {
			lo, hi := samples(f)

			for _, tc := range cases {
				if tc.flag && f.Flag == "" {
					continue // setting has no flag; the case does not apply
				}
				t.Run(tc.name, func(t *testing.T) {
					clearEnv(t)

					value := func(layer string) string {
						if layer == tc.winner {
							return hi
						}
						return lo
					}

					cfgPath := filepath.Join(t.TempDir(), "absent.yaml")
					if tc.file {
						cfgPath = writeConfig(t, f, value("file"))
					}
					if tc.env {
						t.Setenv(f.Envar, value("env"))
					}
					fv := FlagValues{Values: map[string]string{}, SetByUser: map[string]bool{}}
					if tc.flag {
						fv.Values[f.Flag] = value("flag")
						fv.SetByUser[f.Flag] = true
					}

					got, _, err := Load(cfgPath, fv)
					if err != nil {
						t.Fatalf("load: %v", err)
					}

					expected := want(f, hi)
					if tc.winner == "" {
						base := Defaults()
						derive(base)
						expected = f.Get(base)
					}
					if actual := f.Get(got); !reflect.DeepEqual(actual, expected) {
						t.Errorf("%s = %#v, want %#v (%s should win)", f.ConfigPath, actual, expected, tc.winner)
					}
				})
			}
		})
	}
}

// TestFlagNotSetByUserIsIgnored is the precise trap this design exists to
// avoid. kingpin populates a flag's value from its environment variable through
// the same path as a default, so a flag carrying a value is not evidence the
// user asked for it. Only SetByUser is.
func TestFlagNotSetByUserIsIgnored(t *testing.T) {
	clearEnv(t)
	f, _ := FieldByPath("network.nodeIP")
	cfgPath := writeConfig(t, f, "10.0.0.5")

	got, _, err := Load(cfgPath, FlagValues{
		Values:    map[string]string{"node-ip": "192.168.1.1"},
		SetByUser: map[string]bool{}, // present but not set by the user
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Network.NodeIP != "10.0.0.5" {
		t.Errorf("nodeIP = %q, want the file value 10.0.0.5", got.Network.NodeIP)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	clearEnv(t)
	got, warnings, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), FlagValues{})
	if err != nil {
		t.Fatalf("a missing config file must not be an error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if got.Path != types.DefaultBasePath {
		t.Errorf("path = %q, want the default", got.Path)
	}
}

func TestSocketPathDerivedFromPath(t *testing.T) {
	clearEnv(t)
	got, _, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), FlagValues{
		Values:    map[string]string{"path": "/opt/kubesolo"},
		SetByUser: map[string]bool{"path": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/opt/kubesolo/config.sock"; got.API.SocketPath != want {
		t.Errorf("api.socketPath = %q, want %q", got.API.SocketPath, want)
	}
}

func TestUnknownAPIVersionIsRejected(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, []byte("apiVersion: kubesolo.io/v99\nkind: Config\n"), 0o600)

	if _, _, err := Load(path, FlagValues{}); err == nil {
		t.Fatal("expected an error for an unrecognised apiVersion")
	} else if !strings.Contains(err.Error(), "v99") {
		t.Errorf("error should name the offending version, got: %v", err)
	}
}

func TestUnknownFieldWarnsButLoads(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, []byte("apiVersion: kubesolo.io/v1alpha1\nkind: Config\nnetwork:\n  nodeIP: 10.0.0.5\n  futureSetting: yes\n"), 0o600)

	got, warnings, err := Load(path, FlagValues{})
	if err != nil {
		t.Fatalf("an unknown field must not be fatal: %v", err)
	}
	if got.Network.NodeIP != "10.0.0.5" {
		t.Errorf("the rest of the file should still apply, nodeIP = %q", got.Network.NodeIP)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning naming the unrecognised setting")
	}
}

func TestMissingAPIVersionWarns(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, []byte("network:\n  nodeIP: 10.0.0.5\n"), 0o600)

	_, warnings, err := Load(path, FlagValues{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning about the absent apiVersion")
	}
}

func TestMalformedValueIsAnError(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, []byte("apiVersion: kubesolo.io/v1alpha1\nnetwork:\n  mtu: not-a-number\n"), 0o600)

	if _, _, err := Load(path, FlagValues{}); err == nil {
		t.Fatal("a wrongly typed value must be an error, not a warning")
	}
}

func TestBadEnvValueIsAnError(t *testing.T) {
	clearEnv(t)
	t.Setenv("KUBESOLO_MTU", "not-a-number")
	if _, _, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), FlagValues{}); err == nil {
		t.Fatal("expected an error") //nolint
	} else if !strings.Contains(err.Error(), "KUBESOLO_MTU") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}
