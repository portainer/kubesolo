package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/container"
	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
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
	// Not cfg.ContainerImage: install registers that field with a default from
	// KUBESOLO_IMAGE, and registering it here would overwrite the default.
	f.StringVar(&opts.image, "image", "",
		"Container image to upgrade to (container mode; default: portainer/kubesolo:<version>).\n"+
			"Required when the instance runs an image other than portainer/kubesolo")
	return cmd
}

type upgradeOptions struct {
	image         string
	sha256        string
	force         bool
	healthTimeout time.Duration
	detach        bool
}

func runUpgrade(cfg *config.Config, opts upgradeOptions) error {
	p := ui.New()
	p.Header("upgrade")
	cfg.ContainerImage = opts.image

	if containerModeActive(cfg.Name) {
		return runContainerUpgrade(p, cfg, opts)
	}
	if cfg.ContainerImage != "" {
		return p.Fail("upgrade", fmt.Errorf("--image applies to container instances only"))
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

// containerUpgradeImage returns the image a container instance is upgraded to:
// the one given with --image, or the release image of version when the
// instance runs a release image. Any other image, such as a CI build or a
// private mirror, has no release image to follow, so it must be named.
func containerUpgradeImage(current, explicit, version string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if named, err := reference.ParseNormalizedNamed(current); err == nil && reference.FamiliarName(named) == config.DefaultContainerImage {
		return config.DefaultContainerImage + ":" + version, nil
	}
	return "", fmt.Errorf("this instance runs %s, not a %s release image; pass --image with the image to upgrade to", current, config.DefaultContainerImage)
}

// containerUpgradeArgs returns the command line and, when it changes, the
// configuration file of the container an upgrade creates. A container still
// configured by flags moves onto a configuration file, as a host install does on
// upgrade, once the target release reads one; settings that mean nothing in a
// container are left out and returned as dropped. A container already on a
// configuration file keeps it.
func containerUpgradeArgs(oldArgs []string, cfg *config.Config) (args []string, doc []byte, dropped []string, err error) {
	// A custom image is not described by --version; it is taken to be current.
	predatesFile := false
	if cfg.ContainerImage == "" || cfg.ContainerImage == config.DefaultContainerImage+":"+cfg.Version {
		cmp, ok := compareVersions(cfg.Version, config.MinConfigFileVersion)
		predatesFile = ok && cmp < 0
	}
	if config.UsesConfigFile(oldArgs) {
		if predatesFile {
			return nil, nil, nil, fmt.Errorf("%s predates the configuration file this instance runs with, and would not start; upgrade to %s or later",
				cfg.Version, config.MinConfigFileVersion)
		}
		return oldArgs, nil, nil, nil
	}
	if predatesFile {
		return oldArgs, nil, nil, nil
	}
	resolved, _, dropped, _, err := config.ContainerConfig(oldArgs)
	if err != nil {
		return nil, nil, dropped, fmt.Errorf("cannot move the instance's flags onto a configuration file: %w", err)
	}
	if doc, err = kubesoloconfig.Marshal(resolved); err != nil {
		return nil, nil, dropped, err
	}
	return []string{"--config=" + config.ContainerConfigFile}, doc, dropped, nil
}

// checkContainerVersion refuses a target that is not newer than the running
// version unless forced, as a host upgrade does.
func checkContainerVersion(running, target string, force bool) error {
	if force {
		return nil
	}
	cmp, ok := compareVersions(target, running)
	if !ok {
		return fmt.Errorf("cannot compare %s with the running %s; pass --force to upgrade anyway", target, running)
	}
	if cmp <= 0 {
		return fmt.Errorf("%s is not newer than the running %s; pass --force to install it anyway", target, running)
	}
	return nil
}

func runContainerUpgrade(p *ui.Printer, cfg *config.Config, opts upgradeOptions) error {
	unlock, err := service.LockInstance(cfg.Name)
	if err != nil {
		return p.Fail("KubeSolo", err)
	}
	defer unlock()
	if cfg.OfflineInstall != "" || opts.sha256 != "" || opts.detach {
		return p.Fail("upgrade", fmt.Errorf("--offline-install, --sha256 and --detach apply to host installs only; a container upgrade replaces the image"))
	}
	if err := upgrade.ValidateHealthTimeout(int(opts.healthTimeout / time.Second)); err != nil {
		return p.Fail("--health-timeout", err)
	}

	// ── Retrieve current container before replacing it ────────────────────────
	p.Step("Inspecting running container")
	prev, err := service.InspectInstance(cfg.Name)
	if err != nil {
		return p.Fail("container inspect", fmt.Errorf("container %q not found — is KubeSolo installed? (%w)", service.ContainerNameFor(cfg.Name), err))
	}
	current, oldArgs := prev.Config.Image, prev.Config.Cmd
	if cfg.ContainerImage, err = containerUpgradeImage(current, cfg.ContainerImage, cfg.Version); err != nil {
		return p.Fail("image", err)
	}
	running, versionErr := service.ContainerVersion(cfg.Name)
	// A custom image is not described by --version, so only a release image is
	// checked against the running version.
	if cfg.ContainerImage == config.DefaultContainerImage+":"+cfg.Version {
		if versionErr != nil && !opts.force {
			return p.Fail("version", fmt.Errorf("could not read the running version: %w; pass --force to upgrade anyway", versionErr))
		}
		if versionErr == nil {
			if err := checkContainerVersion(running, cfg.Version, opts.force); err != nil {
				return p.Fail("version", err)
			}
		}
	}
	if versionErr == nil {
		p.Info(fmt.Sprintf("Running %s, upgrading to %s", running, cfg.Version))
	}
	newArgs, newDoc, dropped, err := containerUpgradeArgs(oldArgs, cfg)
	if err != nil {
		return p.Fail("configuration", err)
	}
	if newDoc != nil {
		p.Info("Moving the instance's settings from flags onto " + config.ContainerConfigFile)
		for _, f := range dropped {
			p.Warn(f + " is ignored in container mode: kubesoloctl or the container decides it")
		}
	}
	p.OK("Current configuration retrieved", config.AppName)

	// ── Pull the new image while the cluster keeps running ────────────────────
	p.Step(fmt.Sprintf("Upgrading KubeSolo container to %s", cfg.Version))
	if err := service.PullImage(cfg.ContainerImage); err != nil {
		p.Warn("nothing was changed: KubeSolo is still running")
		return p.Fail("image", err)
	}

	// ── Back up the datastore of the stopped container ────────────────────────
	if err := service.StopContainer(cfg.Name); err != nil {
		return p.Fail("container upgrade", err)
	}
	target := service.RollbackTarget{Image: current, ImageID: prev.Image, Args: oldArgs, Version: running, Created: time.Now().UTC()}
	if err := service.BackupDatastore(cfg.Name, prev.Image, target); err != nil {
		if startErr := service.StartContainer(cfg.Name); startErr == nil {
			p.Warn("nothing was changed: the previous container was started again")
		}
		return p.Fail("datastore backup", err)
	}
	p.OK("Datastore backed up", "rollback target: "+current)

	// ── Replace the container, keeping the original runtime flags ─────────────
	err = service.ReplaceContainer(cfg.Name, cfg, newArgs, newDoc, prev)
	var apiAddr string
	var data []byte
	if err == nil {
		apiAddr, data, err = awaitHealthy(cfg.Name, opts.healthTimeout)
	}
	if err != nil {
		return rollBackContainerUpgrade(p, cfg.Name, prev, target, opts.healthTimeout, err)
	}
	if err := service.SettleBackup(cfg.Name, cfg.ContainerImage); err != nil {
		p.Warn("could not drop the previous rollback target: " + err.Error())
	}
	p.OK(fmt.Sprintf("KubeSolo %s container running", cfg.Version), cfg.Name)
	p.OK("Node ready", "https://"+apiAddr)

	// Update kubeconfig: the new container issued its own admin credentials.
	p.Step("Updating kubeconfig")
	if err := kubeconfig.MergeContainerKubeconfig(data, service.ContainerNameFor(cfg.Name), "https://"+apiAddr); err != nil {
		p.Warn("kubeconfig not updated — " + err.Error())
	} else {
		p.OK("Kubeconfig updated", kubeconfig.UserConfigPath())
	}

	p.Done(fmt.Sprintf("KubeSolo upgraded to %s", cfg.Version))
	p.Info("undo it with: kubesoloctl rollback --name " + cfg.Name)
	return nil
}

// rollBackContainerUpgrade puts back the container an upgrade replaced, with the
// datastore as it was, after the new one failed to become healthy, and reports
// the upgrade as failed.
func rollBackContainerUpgrade(p *ui.Printer, name string, prev *container.InspectResponse, target service.RollbackTarget, timeout time.Duration, cause error) error {
	p.Warn("the new version did not become healthy: " + cause.Error())
	if lines, err := service.ContainerLogTail(name, 15); err == nil {
		p.Info("Last lines it logged:")
		for _, l := range lines {
			p.Info("  " + l)
		}
	}

	p.Step("Rolling back to " + target.Image)
	img, err := service.RollbackImage(target)
	if err != nil {
		return p.Fail("rollback", err)
	}
	_ = service.StopContainer(name)
	if err := service.RestoreDatastore(name, img); err != nil {
		return p.Fail("rollback", fmt.Errorf("could not restore the datastore, which is kept in the %s volume under upgrade/container-backup: %w", service.ContainerNameFor(name)+"-data", err))
	}
	restore := &config.Config{Name: name, ContainerImage: img}
	if err := service.ReplaceContainer(name, restore, target.Args, nil, prev); err != nil {
		return p.Fail("rollback", err)
	}
	apiAddr, data, err := awaitHealthy(name, timeout)
	if err != nil {
		p.Info("The backup is kept; retry with: kubesoloctl rollback --name " + name)
		return containerFailed(p, name, fmt.Errorf("the restored version did not become healthy either: %w", err))
	}
	if err := service.FinishRestore(name, img, true); err != nil {
		p.Warn("could not put the earlier rollback target back: " + err.Error())
	}
	if err := kubeconfig.MergeContainerKubeconfig(data, service.ContainerNameFor(name), "https://"+apiAddr); err != nil {
		p.Warn("kubeconfig not updated — " + err.Error())
	}
	p.OK("Rolled back", target.Image)
	return p.Fail("upgrade", fmt.Errorf("the upgrade failed and was rolled back: %w", cause))
}
