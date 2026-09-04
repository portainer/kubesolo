package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/ui"
	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/pkg/components/configapi"
	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func configCmd() *cobra.Command {
	var path string

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and edit the KubeSolo configuration file",
		Long: `Read and edit the KubeSolo configuration file.

KubeSolo reads its settings once at startup, so a change made here takes effect
when KubeSolo restarts.

Examples:
  # Show the whole configuration:
  kubesoloctl config get

  # Show one setting:
  kubesoloctl config get network.nodeIP

  # Change one setting:
  sudo kubesoloctl config set network.nodeIP 10.0.0.5

  # Open the file in $EDITOR, validating before it is saved:
  sudo kubesoloctl config edit

  # Check a file without installing it:
  kubesoloctl config validate -f ./candidate.yaml

  # List every setting, its type and its default:
  kubesoloctl config schema`,
	}

	cmd.PersistentFlags().StringVar(&path, "config", types.DefaultConfigFile, "KubeSolo configuration file")
	cmd.AddCommand(
		configGetCmd(&path),
		configSetCmd(&path),
		configEditCmd(&path),
		configValidateCmd(&path),
		configSchemaCmd(),
	)
	return cmd
}

// backend is where a config command reads and writes.
//
// When KubeSolo is running its configuration API, that is preferred over editing
// the file underneath it: the API validates against the real host, reports which
// settings need a restart, and serialises concurrent writers. A direct file edit
// can do none of those.
type backend struct {
	// api is non-nil when a running KubeSolo answered on its socket.
	api *configapi.Client

	// path is the configuration file, used when api is nil.
	path string

	// file is that file's contents, already read while locating the socket.
	// Reading it a second time would report its warnings twice.
	file *types.Config
}

// resolveBackend decides how to reach the configuration.
//
// The socket path comes from the file itself, since that is what KubeSolo read
// to decide where to listen. A socket file left behind by an unclean shutdown is
// not enough — the client must actually connect.
func resolveBackend(p *ui.Printer, path string) (*backend, error) {
	cfg, err := load(p, path)
	if err != nil {
		return nil, err
	}

	socketPath := cfg.API.SocketPath
	if cfg.API.Enabled && socketPath != "" {
		client := configapi.NewClient(socketPath)
		if client.Available() {
			return &backend{api: client, path: path}, nil
		}
	}

	return &backend{path: path, file: cfg}, nil
}

// reportOutcome tells the operator what a write did, and what still has to
// happen for it to take effect.
func reportOutcome(p *ui.Printer, resp *configapi.Response) {
	for _, w := range resp.Warnings {
		p.Warn(w)
	}
	if len(resp.Changed) == 0 {
		p.Info("no settings changed")
		return
	}
	if resp.RestartRequired {
		p.Info("restart KubeSolo for these to take effect: " + strings.Join(resp.RequiresRestart, ", "))
	}
}

// load reads the configuration file over the built-in defaults.
//
// The environment and the command line are deliberately not applied: this edits
// the file, and folding in layers that only exist at runtime would write their
// values into it permanently.
func load(p *ui.Printer, path string) (*types.Config, error) {
	cfg := kubesoloconfig.Defaults()

	found, warnings, err := kubesoloconfig.Read(path, cfg)
	if err != nil {
		return nil, err
	}
	if !found {
		p.Info(fmt.Sprintf("no configuration file at %s, showing defaults", path))
	}
	for _, w := range warnings {
		p.Warn(w.String())
	}

	return cfg, nil
}

// validationHost describes the machine for validation purposes.
//
// ContainerMode is reported as false because kubesoloctl cannot know whether the
// KubeSolo it is configuring will run inside a container. The consequence is
// narrow: a static CPU manager policy is accepted here and rejected by KubeSolo
// itself at startup, where the real answer is known.
func validationHost() kubesoloconfig.Host {
	return kubesoloconfig.Host{
		NumCPU: runtime.NumCPU(),
		GOARCH: runtime.GOARCH,
	}
}

func configGetCmd(path *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get [path]",
		Short: "Print the configuration, or one setting from it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ui.New()

			b, err := resolveBackend(p, *path)
			if err != nil {
				return err
			}
			cfg, err := b.read(false)
			if err != nil {
				return err
			}

			var value any = cfg
			if len(args) == 1 {
				field, ok := kubesoloconfig.FieldByPath(args[0])
				if !ok {
					return unknownPath(args[0])
				}
				value = field.Get(cfg)
			}

			raw, err := yaml.Marshal(value)
			if err != nil {
				return err
			}
			fmt.Print(string(raw))
			return nil
		},
	}
}

func configSetCmd(path *string) *cobra.Command {
	return &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Change one setting and save the file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, value := args[0], args[1]

			field, ok := kubesoloconfig.FieldByPath(name)
			if !ok {
				return unknownPath(name)
			}

			p := ui.New()

			b, err := resolveBackend(p, *path)
			if err != nil {
				return err
			}

			// The value is applied to a document first, so its typed form is
			// what reaches the API or the file: an integer stays an integer, a
			// list stays a list.
			//
			// Secrets are not fetched. Only the one setting being changed is
			// sent, so a redacted value for any other cannot reach the server —
			// and asking for credentials that will not be used is worth avoiding.
			cfg, err := b.read(false)
			if err != nil {
				return err
			}
			if err := field.Set(cfg, value); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}

			if b.api != nil {
				// Only this one setting is sent, so a concurrent change to any
				// other is preserved rather than overwritten.
				patch, err := kubesoloconfig.MergePatchFor(name, cfg)
				if err != nil {
					return err
				}
				resp, err := b.api.Patch(patch)
				if err != nil {
					return err
				}
				p.OK(fmt.Sprintf("%s set", name), b.describe())
				reportOutcome(p, resp)
				return nil
			}

			// Validation runs before the file is touched, so a rejected value
			// leaves the existing configuration exactly as it was.
			warnings, err := kubesoloconfig.Validate(cfg, validationHost())
			if err != nil {
				return err
			}
			for _, w := range warnings {
				p.Warn(w.String())
			}

			if field.Mutability == kubesoloconfig.MutabilityImmutable {
				p.Warn(fmt.Sprintf(
					"%s cannot be changed on an install that already has state: every certificate, the database and all container state live below it, and none of them move", name))
			}

			if err := kubesoloconfig.Write(*path, cfg); err != nil {
				return err
			}

			p.OK(fmt.Sprintf("%s set", name), *path)
			p.Info("restart KubeSolo for this to take effect")
			return nil
		},
	}
}

func configEditCmd(path *string) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the configuration in $EDITOR, validating before it is saved",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigEdit(*path)
		},
	}
}

func runConfigEdit(path string) error {
	p := ui.New()

	b, err := resolveBackend(p, path)
	if err != nil {
		return err
	}

	// Secrets are fetched unredacted: an editor showing "***" would either
	// discard the credential on save or be rejected for sending the placeholder
	// back. The scratch file is written 0600 accordingly.
	cfg, err := b.read(true)
	if err != nil {
		return err
	}

	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	scratch := filepath.Join(os.TempDir(), "kubesolo-config-*.yaml")
	tmp, err := os.CreateTemp(filepath.Dir(scratch), filepath.Base(scratch))
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// The document carries portainer.edgeKey, so narrow the scratch file before
	// anything is written to it. os.CreateTemp is already 0600 before umask;
	// this holds regardless of the umask kubesoloctl inherited.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := openEditor(tmpName); err != nil {
		return err
	}

	edited := kubesoloconfig.Defaults()
	if _, warnings, err := kubesoloconfig.Read(tmpName, edited); err != nil {
		// The edited file is left in place so the work is not lost.
		return fmt.Errorf("%w\n\nyour edits are kept at %s", err, tmpName)
	} else {
		for _, w := range warnings {
			p.Warn(w.String())
		}
	}

	if b.api != nil {
		resp, err := b.api.Put(edited)
		if err != nil {
			return fmt.Errorf("%w\n\nyour edits are kept at %s", err, tmpName)
		}
		_ = os.Remove(tmpName)

		p.OK("configuration saved", b.describe())
		reportOutcome(p, resp)
		return nil
	}

	warnings, err := kubesoloconfig.Validate(edited, validationHost())
	if err != nil {
		return fmt.Errorf("%w\n\nyour edits are kept at %s", err, tmpName)
	}
	for _, w := range warnings {
		p.Warn(w.String())
	}

	if err := kubesoloconfig.Write(path, edited); err != nil {
		return fmt.Errorf("%w\n\nyour edits are kept at %s", err, tmpName)
	}
	_ = os.Remove(tmpName) // best effort; the edits are already saved

	p.OK("configuration saved", path)
	p.Info("restart KubeSolo for this to take effect")
	return nil
}

// openEditor runs $EDITOR against the file, attached to the terminal.
func openEditor(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	// $EDITOR conventionally carries arguments, e.g. "code --wait".
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...) //nolint:gosec // the editor is the user's own choice
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q exited with an error: %w", editor, err)
	}
	return nil
}

func configValidateCmd(path *string) *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check a configuration file without installing it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ui.New()

			// -f names a candidate file, which is always checked locally: the
			// point is to check it before it is installed anywhere.
			if file != "" {
				cfg, err := load(p, file)
				if err != nil {
					return err
				}
				warnings, err := kubesoloconfig.Validate(cfg, validationHost())
				for _, w := range warnings {
					p.Warn(w.String())
				}
				if err != nil {
					return err
				}
				p.OK("configuration is valid", file)
				return nil
			}

			b, err := resolveBackend(p, *path)
			if err != nil {
				return err
			}

			cfg, err := b.read(true)
			if err != nil {
				return err
			}

			// A running KubeSolo validates against its own host — its CPU count,
			// its architecture, and whether it is in a container. kubesoloctl can
			// only guess at the last of those.
			if b.api != nil {
				resp, err := b.api.Validate(cfg)
				if err != nil {
					return err
				}
				for _, w := range resp.Warnings {
					p.Warn(w)
				}
				p.OK("configuration is valid", b.describe())
				return nil
			}

			warnings, err := kubesoloconfig.Validate(cfg, validationHost())
			for _, w := range warnings {
				p.Warn(w.String())
			}
			if err != nil {
				return err
			}

			p.OK("configuration is valid", *path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Validate this file instead of the installed one")
	return cmd
}

func configSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "List every setting, its type, default and deprecated flag",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// YAML, like every other kubesoloctl output and like the
			// configuration file itself. The HTTP API serves the same content as
			// JSON, which is the convention there.
			raw, err := yaml.Marshal(map[string]any{
				"apiVersion": types.ConfigAPIVersion,
				"settings":   kubesoloconfig.Describe(),
			})
			if err != nil {
				return err
			}
			fmt.Print(string(raw))
			return nil
		},
	}
}

// unknownPath reports an unrecognised setting, naming the closest matches so a
// typo does not require reaching for the documentation.
func unknownPath(name string) error {
	var near []string
	for _, d := range kubesoloconfig.Describe() {
		if strings.Contains(d.Path, name) || strings.Contains(name, strings.Split(d.Path, ".")[0]) {
			near = append(near, d.Path)
		}
	}

	msg := fmt.Sprintf("unknown setting %q", name)
	if len(near) > 0 {
		if len(near) > 5 {
			near = near[:5]
		}
		msg += "\n\ndid you mean:\n  " + strings.Join(near, "\n  ")
	}
	return fmt.Errorf("%s\n\nrun 'kubesoloctl config schema' for the full list", msg)
}

// read loads the configuration from whichever backend was resolved.
//
// withSecrets is honoured only by the API, which redacts by default. A file read
// always has the real values, since the file is the source of them.
func (b *backend) read(withSecrets bool) (*types.Config, error) {
	if b.api != nil {
		resp, err := b.api.Get(withSecrets)
		if err != nil {
			return nil, err
		}
		if resp.Config == nil {
			return nil, fmt.Errorf("the configuration API returned no configuration")
		}
		return resp.Config, nil
	}

	return b.file, nil
}

// describe names the backend, so output says where a value came from.
func (b *backend) describe() string {
	if b.api != nil {
		return "the running KubeSolo"
	}
	return b.path
}
