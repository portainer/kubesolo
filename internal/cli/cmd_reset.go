package cli

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

func resetCmd() *cobra.Command {
	var force bool
	var dataPath string
	var name string
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset KubeSolo cluster to a clean state",
		Long: `Stop the KubeSolo service and wipe all cluster state (database, certificates,
CNI configuration) while preserving the installed binary and service files.
After a reset the service is restarted to initialize a fresh cluster.

This is a destructive operation — use --force to skip the confirmation prompt.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReset(name, dataPath, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	cmd.Flags().StringVar(&dataPath, "path", config.DefaultPath, "KubeSolo data directory")
	cmd.Flags().StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"Name of the KubeSolo instance to reset (default: kubesolo)")
	return cmd
}

func runReset(name, dataPath string, force bool) error {
	if !force {
		fmt.Fprintf(os.Stderr, "\n  This will delete all cluster state in %s.\n", dataPath)
		fmt.Fprint(os.Stderr, "  Are you sure? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(os.Stderr, "\n  Reset cancelled.")
			return nil
		}
	}

	p := ui.New()
	p.Header("reset")

	// Container mode on macOS — no root required.
	if runtime.GOOS == "darwin" {
		return runContainerReset(p, name)
	}

	if err := preflight.CheckRoot(); err != nil {
		return p.Fail("root check", err)
	}

	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}

	p.Step("Stopping KubeSolo")
	process.StopAll(initControlBinary(info.InitSystem))
	p.OK("KubeSolo stopped", "")

	p.Step("Removing cluster state")
	if err := os.RemoveAll(dataPath); err != nil {
		return p.Fail("removing data directory", err)
	}
	p.OK("Cluster state cleared", dataPath)

	p.Step("Starting fresh cluster")
	if err := runServiceAction(info.InitSystem, "start"); err != nil {
		p.Warn("could not restart service — start it manually")
	} else {
		p.OK("KubeSolo restarted", "")
	}

	p.Done("Reset complete — fresh cluster initializing.")
	return nil
}

func runContainerReset(p *ui.Printer, name string) error {
	p.Step("Resetting KubeSolo container")
	if err := service.ResetContainer(name); err != nil {
		return p.Fail("container reset", err)
	}
	p.OK("Fresh container started", name)

	p.Done("Reset complete — fresh cluster initializing.")
	return nil
}
