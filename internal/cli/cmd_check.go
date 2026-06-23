package cli

import (
	"fmt"
	"runtime"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

func checkCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run pre-flight checks without installing",
		Long:  `Validates that this host meets all requirements for KubeSolo. Exits 0 on success.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cfg)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&cfg.InstallPrereqs, "install-prereqs",
		envBool("KUBESOLO_INSTALL_PREREQS", false),
		"Automatically install missing OS prerequisites (e.g. nftables on Alpine)")
	f.BoolVar(&cfg.PprofServer, "pprof-server",
		envBool("KUBESOLO_PPROF_SERVER", false),
		"Include pprof port 6060 in port conflict checks")
	return cmd
}

func runCheck(cfg *config.Config) error {
	p := ui.New()
	p.Header("check")

	p.Step("Detecting system")
	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}

	var checks []preflight.Check
	if runtime.GOOS == "darwin" {
		p.OK("System detected", fmt.Sprintf("%s/%s · container mode", info.OS, info.Arch))
		checks = preflight.ContainerSuite()
	} else {
		p.OK("System detected", fmt.Sprintf("%s · %s · %s", info.Arch, info.LibC, info.InitSystem))
		checks = preflight.Suite(cfg.InstallPrereqs, cfg.PprofServer)
	}

	p.Step("Pre-flight checks")
	if err := preflight.RunSuite(checks); err != nil {
		return p.Fail("pre-flight checks", err)
	}
	p.OK(fmt.Sprintf("All %d checks passed", len(checks)), "")

	p.Done("Host is ready for KubeSolo.")
	return nil
}
