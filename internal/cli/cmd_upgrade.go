package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/spf13/cobra"
)

func upgradeCmd(cfg *config.Config) *cobra.Command {
	var opts upgradeOptions
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade KubeSolo to a newer version",
		Long: `Upgrade KubeSolo in place, with a backup and automatic rollback.

Everything that can fail is checked while KubeSolo keeps running: the release is
downloaded and its checksum verified, the binary is checked against this host's
architecture and C library, the datastore is backed up, and the new version
opens a copy of it. Only then is KubeSolo stopped and the binary replaced. If
the new version does not become healthy, the previous binary, datastore and
configuration are restored automatically.

When KubeSolo serves its API, the upgrade goes through it, so it cannot collide
with one Portainer started. Otherwise kubesoloctl runs the same upgrade itself.

Examples:
  sudo kubesoloctl upgrade --version=v1.2.2
  sudo kubesoloctl upgrade --version=v1.2.2 --offline-install=/tmp/kubesolo-v1.2.2-linux-amd64.tar.gz`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cfg, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Version, "version", "", "KubeSolo version to upgrade to (required)")
	_ = cmd.MarkFlagRequired("version")
	f.StringVar(&cfg.OfflineInstall, "offline-install", "",
		"Path to a local release archive or binary to install instead of downloading")
	f.StringVar(&opts.sha256, "sha256", "",
		"Expected SHA-256 of the archive or binary (default: taken from the release's SHA256SUMS)")
	f.BoolVar(&opts.force, "force", false, "Allow installing a version that is not newer than the installed one")
	f.DurationVar(&opts.healthTimeout, "health-timeout", upgrade.DefaultHealthTimeout,
		"How long the new version has to become healthy before it is rolled back")
	f.BoolVar(&opts.detach, "detach", false, "Return once the upgrade has started instead of following it")
	f.StringVar(&cfg.Name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"Name of the KubeSolo instance to upgrade (default: kubesolo)")
	return cmd
}

type upgradeOptions struct {
	sha256        string
	force         bool
	healthTimeout time.Duration
	detach        bool
}

func runUpgrade(cfg *config.Config, opts upgradeOptions) error {
	p := ui.New()
	p.Header("upgrade")

	if containerModeActive(cfg.Name) {
		return runContainerUpgrade(p, cfg)
	}

	if err := preflight.CheckRoot(); err != nil {
		return p.Fail("root check", err)
	}

	req := upgrade.Request{
		Version:              cfg.Version,
		SHA256:               opts.sha256,
		Force:                opts.force,
		HealthTimeoutSeconds: int(opts.healthTimeout / time.Second),
	}
	if cfg.OfflineInstall != "" {
		abs, err := filepath.Abs(cfg.OfflineInstall)
		if err != nil {
			return p.Fail("offline source", err)
		}
		req.Source = abs
	}
	if err := req.Validate(); err != nil {
		return p.Fail("upgrade request", err)
	}

	h, err := findHost()
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	from, err := installedVersion()
	if err != nil {
		return p.Fail("installed version", err)
	}

	var id string
	if h.api != nil {
		p.Step(fmt.Sprintf("Requesting the upgrade from %s to %s from KubeSolo", from, req.Version))
		accepted, err := h.api.Upgrade(req)
		if err != nil {
			return p.Fail("upgrade request", err)
		}
		id = accepted.ID
	} else {
		if id, err = startLocalUpgrade(p, h, req, from); err != nil {
			return err
		}
	}
	p.OK("Upgrade started", "run "+id)

	if opts.detach {
		p.Info("follow it with: kubesoloctl status")
		return nil
	}

	p.Step("Upgrading")
	run, err := follow(p, h.layout, id)
	if err != nil {
		return p.Fail("upgrade", err)
	}
	if err := reportRun(p, run); err != nil {
		return err
	}

	// Releases from v1.2.1 read a configuration file. A host still configured
	// by flags in its service definition is moved onto one now.
	if v, err := upgrade.ParseVersion(req.Version); err == nil && v.Compare(configFileSince) >= 0 {
		if info, err := detect.Detect(); err == nil {
			migrateToConfigFile(p, cfg, info)
		}
	}

	p.Done(fmt.Sprintf("KubeSolo upgraded from %s to %s", run.From, run.To))
	return nil
}

// configFileSince is the first release that reads a configuration file.
var configFileSince, _ = upgrade.ParseVersion("v1.2.1")

// startLocalUpgrade stages the release and starts the executor without the
// API. It stages here, rather than in the executor, so that the executor can
// be the new binary: a KubeSolo from before the executor existed cannot run
// one, but the release replacing it can.
func startLocalUpgrade(p *ui.Printer, h *host, req upgrade.Request, from string) (string, error) {
	info, err := detect.Detect()
	if err != nil {
		return "", p.Fail("system detection", err)
	}

	id := upgrade.NewRunID()
	staging := h.layout.RunStagingDir("ctl-" + id)
	// Once the executor starts it owns the staging directory and removes it
	// when it finishes; until then, a failure here must clean it up.
	started := false
	defer func() {
		if !started {
			_ = os.RemoveAll(staging)
		}
	}()

	p.Step(fmt.Sprintf("Fetching and verifying KubeSolo %s", req.Version))
	staged, err := upgrade.Stage(context.Background(), h.layout, staging, req, info,
		func(format string, args ...any) { p.Info(fmt.Sprintf(format, args...)) })
	if err != nil {
		p.Warn("nothing was changed: KubeSolo " + from + " is still running")
		return "", p.Fail("release verification", err)
	}
	p.OK("Release verified", staged.ChecksumSource)

	executor := staged.Binary
	if !supportsExecutor(executor) {
		executor = upgrade.BinaryPath
		if !supportsExecutor(executor) {
			return "", p.Fail("upgrade", fmt.Errorf("neither %s nor the installed %s can run the upgrade; upgrade to a newer release first", req.Version, from))
		}
	}

	// The executor installs the binary staged here: a hard link of it, so no
	// second copy, verified again against its own checksum.
	sum, err := upgrade.FileSHA256(staged.Binary)
	if err != nil {
		return "", p.Fail("upgrade", err)
	}
	local := req
	local.Source, local.SHA256 = staged.Binary, sum

	job := upgrade.Job{
		ID: id, Operation: upgrade.OpUpgrade, Request: local,
		DataDir: h.layout.DataDir, ConfigFile: h.layout.ConfigFile, From: from,
	}
	if err := startLocally(h, job, executor, info); err != nil {
		return "", p.Fail("upgrade", err)
	}
	started = true
	return id, nil
}

func runContainerUpgrade(p *ui.Printer, cfg *config.Config) error {
	// ── Retrieve current container CMD before replacing it ────────────────────
	p.Step("Inspecting running container")
	oldArgs, err := service.ContainerArgs(cfg.Name)
	if err != nil {
		return p.Fail("container inspect", err)
	}
	// Container mode always runs with upstream defaults (--full); ensure it
	// survives upgrades of containers created before that became the default.
	oldArgs = ensureArg(oldArgs, "--full")
	p.OK("Current configuration retrieved", config.AppName)

	// ── Pull new image, stop old container, start fresh ───────────────────────
	p.Step(fmt.Sprintf("Upgrading KubeSolo container to %s", cfg.Version))

	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}
	mgr, err := service.New(info, config.RunModeContainer, cfg.Name)
	if err != nil {
		return p.Fail("container upgrade", err)
	}

	// Install with the new version but preserve the original runtime flags.
	if err := mgr.Install(cfg, oldArgs); err != nil {
		return p.Fail("container upgrade", err)
	}
	p.OK(fmt.Sprintf("KubeSolo %s container running", cfg.Version), cfg.Name)

	// Update kubeconfig with the new container's ephemeral port.
	p.Step("Updating kubeconfig")
	cname := service.ContainerNameFor(cfg.Name)
	if port, err := service.GetContainerAPIPort(cfg.Name); err == nil {
		apiAddr := fmt.Sprintf("127.0.0.1:%d", port)
		if data, err := kubeconfig.WaitForContainerKubeconfig(cname, ""); err == nil {
			kubeconfig.MergeContainerKubeconfig(data, cfg.Name, "https://"+apiAddr)
		}
		if err := kubeconfig.WaitForAPIServer(apiAddr, 120*time.Second); err != nil {
			p.Warn(fmt.Sprintf("API server not yet ready at https://%s", apiAddr))
		} else {
			p.OK("API server ready", "https://"+apiAddr)
		}
	} else {
		p.Warn("could not discover container API port — run: kubesoloctl kubeconfig fetch")
	}

	p.Done(fmt.Sprintf("KubeSolo upgraded to %s", cfg.Version))
	return nil
}
