package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/config/flags"
	"github.com/portainer/kubesolo/types"
)

// flagsNotInRegistry are KubeSolo flags that deliberately have no config
// setting. Kingpin's own built-ins (--help, --completion-*) need no entry here:
// they carry no environment variable, which is what this test keys off.
var flagsNotInRegistry = map[string]bool{
	"full":   true, // deprecated no-op, retained only for compatibility
	"config": true, // names the config file, so it cannot live inside it
}

// TestRegistryCoversEveryFlag is the guard that keeps the two from drifting: a
// flag added without a config setting, or a setting whose flag was renamed,
// fails here rather than silently becoming unreachable from the config file.
//
// Scope is every flag carrying a KUBESOLO_ environment variable. That is every
// configurable flag — --version is the only KubeSolo flag without one, and it
// prints and exits rather than configuring anything.
func TestRegistryCoversEveryFlag(t *testing.T) {
	inRegistry := map[string]bool{}
	for _, f := range Registry() {
		if f.Flag != "" {
			inRegistry[f.Flag] = true
		}
	}

	declared := map[string]bool{}
	for _, m := range flags.Application.Model().Flags {
		if !strings.HasPrefix(m.Envar, "KUBESOLO_") {
			continue
		}
		declared[m.Name] = true
		if flagsNotInRegistry[m.Name] {
			continue
		}
		if !inRegistry[m.Name] {
			t.Errorf("flag --%s has no entry in Registry(); add one or list it in flagsNotInRegistry", m.Name)
		}
	}

	for flag := range inRegistry {
		if !declared[flag] {
			t.Errorf("Registry() references --%s, which is not declared in internal/config/flags", flag)
		}
	}
}

// TestRegistryEnvarsMatchFlags pins the environment variable names against the
// flags they mirror, so the loader's manual os.LookupEnv cannot drift from what
// kingpin advertises.
func TestRegistryEnvarsMatchFlags(t *testing.T) {
	declared := map[string]string{}
	for _, m := range flags.Application.Model().Flags {
		declared[m.Name] = m.Envar
	}
	for _, f := range Registry() {
		if f.Flag == "" {
			continue
		}
		if got := declared[f.Flag]; got != f.Envar {
			t.Errorf("%s: registry envar %q, flag --%s declares %q", f.ConfigPath, f.Envar, f.Flag, got)
		}
	}
}

// TestRegistryPathsAreUnique catches a copy-paste slip in the table, which
// would otherwise make one of the two settings unreachable by path.
func TestRegistryPathsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Registry() {
		if seen[f.ConfigPath] {
			t.Errorf("duplicate config path %q", f.ConfigPath)
		}
		seen[f.ConfigPath] = true
	}
}

func TestFieldByPath(t *testing.T) {
	if f, ok := FieldByPath("network.nodeIP"); !ok || f.Flag != "node-ip" {
		t.Errorf("FieldByPath(network.nodeIP) = %+v, %v", f, ok)
	}
	if _, ok := FieldByPath("no.such.setting"); ok {
		t.Error("FieldByPath returned ok for an unknown path")
	}
}

// TestOnlyPathIsImmutable pins the mutability classification. Path is the one
// setting that cannot be changed on an existing install; anything else becoming
// immutable is a decision that should be made deliberately, not by a typo.
func TestOnlyPathIsImmutable(t *testing.T) {
	for _, f := range Registry() {
		if f.Mutability == MutabilityImmutable && f.ConfigPath != "path" {
			t.Errorf("%s is marked immutable; only path should be", f.ConfigPath)
		}
		if f.ConfigPath == "path" && f.Mutability != MutabilityImmutable {
			t.Error("path must be immutable")
		}
	}
}

// TestSecretsAreMarked pins which settings the API redacts.
func TestSecretsAreMarked(t *testing.T) {
	want := map[string]bool{"portainer.edgeKey": true}
	for _, f := range Registry() {
		if f.Secret != want[f.ConfigPath] {
			t.Errorf("%s: Secret = %v, want %v", f.ConfigPath, f.Secret, want[f.ConfigPath])
		}
	}
}

func TestCheckSetOnce(t *testing.T) {
	agent := Defaults()
	agent.Portainer.EdgeID, agent.Portainer.EdgeKey = "edge-id", "edge-key"

	change := func(edit func(*types.Config)) *types.Config {
		next := *agent
		edit(&next)
		return &next
	}
	cases := []struct {
		name    string
		current *types.Config
		next    *types.Config
		field   string
	}{
		{"no agent yet: the agent can be configured", Defaults(), agent, ""},
		{"edge ID changed", agent, change(func(c *types.Config) { c.Portainer.EdgeID = "other" }), "portainer.edgeID"},
		{"edge key changed", agent, change(func(c *types.Config) { c.Portainer.EdgeKey = "other" }), "portainer.edgeKey"},
		{"async changed", agent, change(func(c *types.Config) { c.Portainer.Async = true }), "portainer.async"},
		{"agent removed", agent, Defaults(), "portainer.edgeID"},
		{"other settings", agent, change(func(c *types.Config) { c.Portainer.Image = "portainer/agent:2.31.0"; c.Network.MTU = 1400 }), ""},
	}
	for _, c := range cases {
		err := CheckSetOnce(c.current, c.next)
		var setOnce *SetOnceError
		switch {
		case c.field == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.field != "" && (!errors.As(err, &setOnce) || setOnce.Field != c.field):
			t.Errorf("%s: err = %v, want %s", c.name, err, c.field)
		}
	}

	for _, d := range Describe() {
		want := "restart"
		switch d.Path {
		case "path":
			want = "immutable"
		case "portainer.edgeID", "portainer.edgeKey", "portainer.async":
			want = "set-once"
		}
		if d.Mutability != want {
			t.Errorf("%s: mutability %q, want %q", d.Path, d.Mutability, want)
		}
	}
}
