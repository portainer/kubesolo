package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/portainer/kubesolo/types"
)

// Warning is something worth telling the operator about that is not fatal.
type Warning struct {
	// Field is the config path the warning concerns, empty if it concerns the
	// document as a whole.
	Field   string
	Message string
}

func (w Warning) String() string {
	if w.Field == "" {
		return w.Message
	}
	return w.Field + ": " + w.Message
}

// FlagValues carries the parsed command line into the loader.
//
// SetByUser is the part that matters. kingpin marks a flag as set by the user
// only when it came from the command line; a value supplied through the
// environment is applied via the same path as a default and is indistinguishable
// from one. Environment variables are therefore resolved here, directly against
// os.LookupEnv, rather than read back off the flag.
type FlagValues struct {
	Values    map[string]string
	SetByUser map[string]bool
}

// Load resolves the effective configuration.
//
// Precedence, lowest to highest: built-in defaults, the config file, the
// environment, the command line. Each layer only overwrites the settings it
// actually specifies.
func Load(cfgPath string, fv FlagValues) (*types.Config, []Warning, error) {
	return load(cfgPath, fv, true)
}

// LoadWithoutEnv is Load with the environment layer omitted.
//
// It exists for kubesoloctl, which builds a configuration file for a service it
// is about to install. The environment kubesoloctl happens to run in is not the
// one that service will run in, and folding it in would write values into the
// file that the operator never asked for.
func LoadWithoutEnv(cfgPath string, fv FlagValues) (*types.Config, []Warning, error) {
	return load(cfgPath, fv, false)
}

func load(cfgPath string, fv FlagValues, withEnv bool) (*types.Config, []Warning, error) {
	cfg := Defaults()

	_, warnings, err := Read(cfgPath, cfg)
	if err != nil {
		return nil, nil, err
	}

	if withEnv {
		if err := applyEnv(cfg); err != nil {
			return nil, nil, err
		}
	}

	if err := applyFlags(cfg, fv); err != nil {
		return nil, nil, err
	}

	derive(cfg)
	return cfg, warnings, nil
}

// applyEnv overlays every setting whose environment variable is present. An
// empty value counts as set: clearing a setting that the config file populated
// is a legitimate thing to want from the environment.
func applyEnv(cfg *types.Config) error {
	for _, f := range Registry() {
		if f.Envar == "" {
			continue
		}
		v, ok := os.LookupEnv(f.Envar)
		if !ok {
			continue
		}
		if err := f.Set(cfg, v); err != nil {
			return fmt.Errorf("%s: %w", f.Envar, err)
		}
	}
	return nil
}

// applyFlags overlays every setting the user named on the command line.
func applyFlags(cfg *types.Config, fv FlagValues) error {
	for _, f := range Registry() {
		if f.Flag == "" || !fv.SetByUser[f.Flag] {
			continue
		}
		if err := f.Set(cfg, fv.Values[f.Flag]); err != nil {
			return fmt.Errorf("--%s: %w", f.Flag, err)
		}
	}
	return nil
}

// derive fills in the settings whose default depends on another setting, and so
// cannot be expressed in Defaults().
func derive(cfg *types.Config) {
	if cfg.API.SocketPath == "" {
		cfg.API.SocketPath = filepath.Join(cfg.Path, types.DefaultAPISocketName)
	}
}
