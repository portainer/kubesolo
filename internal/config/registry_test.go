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

// TestImmutableSettings pins the mutability classification. Path cannot be
// changed on an existing install, nor can the cluster networks, which the node,
// every Service and the API server certificate were given addresses from;
// anything else becoming immutable is a decision that should be made
// deliberately, not by a typo.
func TestImmutableSettings(t *testing.T) {
	immutable := map[string]bool{
		"path":                   true,
		"kubernetes.podCIDR":     true,
		"kubernetes.serviceCIDR": true,
		"kubernetes.clusterDNS":  true,
	}
	for _, f := range Registry() {
		if got := f.Mutability == MutabilityImmutable; got != immutable[f.ConfigPath] {
			t.Errorf("%s: immutable = %v, want %v", f.ConfigPath, got, immutable[f.ConfigPath])
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

// TestKeepImmutable is what lets a reset of the whole document through its own
// immutability check: every immutable setting is carried over, nothing else is.
func TestKeepImmutable(t *testing.T) {
	current := Defaults()
	current.Path = "/data/kubesolo"
	current.Kubernetes.PodCIDR = "10.50.0.0/16"
	current.Kubernetes.ServiceCIDR = "10.51.0.0/16"
	current.Kubernetes.ClusterDNS = "10.51.0.53"
	current.Logging.Debug = true

	next := Defaults()
	KeepImmutable(next, current)

	if next.Path != current.Path || next.Kubernetes.PodCIDR != current.Kubernetes.PodCIDR ||
		next.Kubernetes.ServiceCIDR != current.Kubernetes.ServiceCIDR || next.Kubernetes.ClusterDNS != current.Kubernetes.ClusterDNS {
		t.Errorf("immutable settings not carried over: %+v", next.Kubernetes)
	}
	if next.Logging.Debug {
		t.Error("a mutable setting was carried over")
	}
	for _, f := range Registry() {
		if f.Mutability == MutabilityImmutable && f.Unchangeable == "" {
			t.Errorf("%s is immutable but does not say why", f.ConfigPath)
		}
	}
}

func TestKeepUnflagged(t *testing.T) {
	from := Defaults()
	from.Path = "/data/kubesolo"
	from.API.SocketPath = "/data/kubesolo/" + types.DefaultAPISocketName
	from.Kubernetes.Webhook.Port = 11443

	cfg := Defaults()
	cfg.Path = "/data/kubesolo"
	KeepUnflagged(cfg, from)
	if cfg.Kubernetes.Webhook.Port != 11443 || cfg.API.SocketPath != from.API.SocketPath {
		t.Errorf("not carried over: webhook port %d, socket %s", cfg.Kubernetes.Webhook.Port, cfg.API.SocketPath)
	}

	// Under a new path the socket moves with it, unless it was put elsewhere.
	cfg = Defaults()
	cfg.Path = "/srv/kubesolo"
	KeepUnflagged(cfg, from)
	if want := "/srv/kubesolo/" + types.DefaultAPISocketName; cfg.API.SocketPath != want {
		t.Errorf("socket %s, want %s", cfg.API.SocketPath, want)
	}
	from.API.SocketPath = "/run/kubesolo.sock"
	KeepUnflagged(cfg, from)
	if cfg.API.SocketPath != "/run/kubesolo.sock" {
		t.Errorf("a socket put elsewhere moved to %s", cfg.API.SocketPath)
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
		case "path", "kubernetes.podCIDR", "kubernetes.serviceCIDR", "kubernetes.clusterDNS":
			want = "immutable"
		case "portainer.edgeID", "portainer.edgeKey", "portainer.async":
			want = "set-once"
		}
		if d.Mutability != want {
			t.Errorf("%s: mutability %q, want %q", d.Path, d.Mutability, want)
		}
	}
}
