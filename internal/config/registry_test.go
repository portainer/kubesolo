package config

import (
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/config/flags"
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
