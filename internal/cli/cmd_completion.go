package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func completionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for kubesoloctl.

To load completions:

Bash:
  $ source <(kubesoloctl completion bash)
  # To load completions for each session, execute once:
  $ kubesoloctl completion bash > /etc/bash_completion.d/kubesoloctl

Zsh:
  $ source <(kubesoloctl completion zsh)
  # To load completions for each session, execute once:
  $ kubesoloctl completion zsh > "${fpath[1]}/_kubesoloctl"

Fish:
  $ kubesoloctl completion fish | source
  # To load completions for each session, execute once:
  $ kubesoloctl completion fish > ~/.config/fish/completions/kubesoloctl.fish`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
}
