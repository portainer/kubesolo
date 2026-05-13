package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print kubesoloctl version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kubesoloctl %s (commit %s, built %s)\n", version, commit, buildDate)
		},
	}
}
