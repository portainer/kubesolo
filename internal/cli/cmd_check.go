package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/ui"
	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
)

func checkCmd(cfg *config.Config) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run pre-flight checks without installing",
		Long:  `Validates that this host meets all requirements for KubeSolo. Exits 0 on success.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				return runCheckJSON(cfg)
			}
			return runCheck(cfg)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&asJSON, "json", false,
		"Run every check, even after one fails, and print each result as JSON")
	f.BoolVar(&cfg.InstallPrereqs, "install-prereqs",
		envBool("KUBESOLO_INSTALL_PREREQS", false),
		"Automatically install missing OS prerequisites (e.g. iptables/nftables)")
	f.BoolVar(&cfg.PprofServer, "pprof-server",
		envBool("KUBESOLO_PPROF_SERVER", false),
		"Include the pprof port (logging.pprofPort, 6060 by default) in port conflict checks")
	f.StringVar(&cfg.NodeIP, "node-ip",
		os.Getenv("KUBESOLO_NODE_IP"),
		"Node IP the install will use, for the host firewall check. Defaults to auto-detection")
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
		checks = preflight.Suite(kubesoloconfig.Installed(types.DefaultConfigFile), cfg.InstallPrereqs, cfg.PprofServer, cfg.NodeIP)
	}

	p.Step("Pre-flight checks")
	if err := preflight.RunSuite(checks); err != nil {
		return p.Fail("pre-flight checks", err)
	}
	p.OK(fmt.Sprintf("All %d checks passed", len(checks)), "")

	p.Done("Host is ready for KubeSolo.")
	return nil
}

func runCheckJSON(cfg *config.Config) error {
	checks := preflight.ContainerSuite()
	if runtime.GOOS != "darwin" {
		checks = preflight.Suite(kubesoloconfig.Installed(types.DefaultConfigFile), cfg.InstallPrereqs, cfg.PprofServer, cfg.NodeIP)
	}
	return writeCheckReport(os.Stdout, preflight.Report(checks))
}

// writeCheckReport prints report and, when a check failed, returns an error
// with nothing more to say so the command exits non-zero.
func writeCheckReport(w io.Writer, report types.PreflightReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return err
	}
	if report.Failed() {
		return ui.ErrAlreadyReported
	}
	return nil
}
