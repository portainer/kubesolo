package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/internal/config/flags"
	"github.com/portainer/kubesolo/internal/core/embedded"
	"github.com/portainer/kubesolo/internal/core/pki"
	"github.com/portainer/kubesolo/internal/logging"
	"github.com/portainer/kubesolo/internal/runtime/cri"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/internal/upgrade/executor"
	"github.com/portainer/kubesolo/pkg/components/configapi"
	"github.com/portainer/kubesolo/pkg/components/coredns"
	"github.com/portainer/kubesolo/pkg/components/d2k"
	"github.com/portainer/kubesolo/pkg/components/localpath"
	"github.com/portainer/kubesolo/pkg/components/metrics"
	"github.com/portainer/kubesolo/pkg/components/portainer"
	"github.com/portainer/kubesolo/pkg/kine"
	"github.com/portainer/kubesolo/pkg/kubernetes/apiserver"
	"github.com/portainer/kubesolo/pkg/kubernetes/controller"
	"github.com/portainer/kubesolo/pkg/kubernetes/kubelet"
	"github.com/portainer/kubesolo/pkg/kubernetes/kubeproxy"
	kubesoloruntime "github.com/portainer/kubesolo/pkg/runtime"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"sigs.k8s.io/yaml"
)

var (
	Version   = "dev"
	BuildDate = "unknown"
	Commit    = "unknown"
)

// the main struct for the kubesolo application
type kubesolo struct {
	wg       sync.WaitGroup
	hostName string

	// cfg is the resolved configuration: defaults, overlaid by the config file,
	// then the environment, then the command line.
	cfg *types.Config

	// runtimeEndpoint is cfg.Runtime.Endpoint parsed. Empty means the embedded
	// containerd, whose socket lives under cfg.Path.
	runtimeEndpoint cri.Endpoint

	embedded types.Embedded
}

// the channels for the kubesolo application
var (
	runtimeReadyCh    = make(chan struct{})
	kineReadyCh       = make(chan struct{})
	apiServerReadyCh  = make(chan struct{})
	kubeletReadyCh    = make(chan struct{})
	controllerReadyCh = make(chan struct{})
	kubeproxyReadyCh  = make(chan struct{})
	metricsReadyCh    = make(chan struct{})
	configAPIReadyCh  = make(chan struct{})
)

// service resolves the configuration and builds the application from it.
//
// Every check that used to live here now lives in config.Validate, which returns
// errors instead of exiting so that the config API can reject a bad payload
// without taking the cluster down.
func service() (*kubesolo, error) {
	cfg, warnings, err := config.Load(*flags.Config, config.FlagValues{
		Values:    flags.Values(),
		SetByUser: flags.SetByUser(),
	})
	if err != nil {
		return nil, err
	}

	validationWarnings, err := config.Validate(cfg, config.Host{
		NumCPU:        runtime.NumCPU(),
		GOARCH:        runtime.GOARCH,
		ContainerMode: system.IsRunningInContainer(),
	})
	if err != nil {
		return nil, err
	}

	for _, w := range append(warnings, validationWarnings...) {
		log.Warn().Str("component", "kubesolo").Msg(w.String())
	}

	runtimeEndpoint, err := cri.Resolve(cfg.Runtime.Endpoint)
	if err != nil {
		return nil, err
	}

	return &kubesolo{
		hostName:        system.GetHostname(),
		cfg:             cfg,
		runtimeEndpoint: runtimeEndpoint,
	}, nil
}

// main is the entry point for the kubesolo application
// it parses the command line arguments and creates a new kubesolo application
// it then bootstraps the application and runs it
// it also handles the shutdown of the application by listening for interrupt signals
// and shutting down the application gracefully
func main() {
	kingpin.MustParse(flags.Application.Parse(os.Args[1:]))

	if *flags.Version {
		log.Info().Str("version", Version).Msg("kubesolo version")
		os.Exit(0)
	}

	// The upgrade runs the binary as its own helpers. Neither is KubeSolo, so
	// they are dispatched before any configuration is loaded or validated.
	if *flags.UpgradeExecutor != "" {
		os.Exit(executor.Main(*flags.UpgradeExecutor))
	}
	if *flags.UpgradeCheckDatastore != "" {
		if err := executor.CheckDatastore(*flags.UpgradeCheckDatastore); err != nil {
			fmt.Fprintln(os.Stderr, "datastore check failed:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *flags.Full {
		log.Warn().Str("component", "kubesolo").Msg("the --full flag (KUBESOLO_FULL) is deprecated and has no effect; KubeSolo always uses upstream Kubernetes defaults")
	}

	service, err := service()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create service. check the logs for more information. exiting...")
	}

	// --print-config is answered before anything is touched. It is the migration
	// path off flags: run the binary with the flags an existing service unit
	// passes and save the result as the config file.
	if *flags.PrintConfig {
		if err := printConfig(service.cfg); err != nil {
			log.Fatal().Err(err).Msg("failed to print configuration")
		}
		os.Exit(0)
	}

	if service.cfg.Kubernetes.APIServer.StartupTimeoutSeconds > 0 {
		types.DefaultRetryCount = service.cfg.Kubernetes.APIServer.StartupTimeoutSeconds / int(types.DefaultComponentSleep.Seconds())
	}

	service.bootstrap()
	service.run()
}

// printConfig writes the resolved configuration to stdout.
func printConfig(cfg *types.Config) error {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(raw)
	return err
}

// run is the main function for the kubesolo application
// the list of services; containerd, kine, apiserver, controller, kubelet, kubeproxy is started in the order of dependency
// coredns and portainer edge agent (only when the portainer edge id and key are provided) are deployed last
func (s *kubesolo) run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info().Msg("the main process received interrupt signal, shutting down...")
		cancel()
	}()

	log.Info().
		Str("version", Version).
		Str("build-date", BuildDate).
		Str("commit", Commit).
		Msg("starting kubesolo...")

	log.Info().Str("component", "kubesolo").Msg("ensuring all embedded dependencies are available...")
	if err := embedded.EnsureEmbeddedDependencies(s.embedded); err != nil {
		log.Fatal().Err(err).Msg("failed to ensure embedded dependencies")
	}

	log.Info().Str("component", "kubesolo").Msg("checking PKI validity against current node IPs...")
	if err := pki.InvalidateIfStale(s.embedded); err != nil {
		log.Fatal().Err(err).Msg("failed to invalidate PKI directory")
	}

	log.Info().Str("component", "kubesolo").Msg("generating relevant certificates...")
	if err := pki.GenerateAllCertificates(s.embedded); err != nil {
		log.Fatal().Err(err).Msg("failed to generate full certificates")
	}
	log.Info().Str("component", "kubesolo").Msg("starting kubesolo services... this may take a few minutes...")

	type service struct {
		name    string
		start   func(ctx context.Context, cancel context.CancelFunc)
		readyCh chan struct{}
	}

	// Each service runs with its own context so shutdown can stop them one at
	// a time. The cancel it is handed also cancels ctx, so a service that fails
	// still brings the whole process down.
	type startedService struct {
		name string
		stop context.CancelFunc
		done chan struct{}
	}
	var started []startedService
	startService := func(svc service) {
		svcCtx, svcCancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		// Not tracked by s.wg: stopServices waits on done with a deadline, and a
		// service that misses it must not block the s.wg.Wait() below.
		go func() {
			defer close(done)
			svc.start(svcCtx, func() {
				svcCancel()
				cancel()
			})
		}()
		started = append(started, startedService{name: svc.name, stop: svcCancel, done: done})
	}

	// stopServices stops the started services in reverse start order, so each one
	// goes down while the services it depends on are still running. A service that
	// has not returned within serviceStopTimeout is left to process exit: the
	// embedded containerd only returns from its own SIGTERM handler, so it never
	// stops when shutdown was not triggered by a signal.
	stopServices := func() {
		for i := len(started) - 1; i >= 0; i-- {
			log.Info().Str("component", "kubesolo").Msgf("stopping %s...", started[i].name)
			started[i].stop()
			select {
			case <-started[i].done:
			case <-time.After(serviceStopTimeout):
				log.Warn().Str("component", "kubesolo").Msgf("%s did not stop within %s, leaving it to process exit", started[i].name, serviceStopTimeout)
			}
		}
	}

	// stopStarted handles shutdown before every service is ready. The service that
	// never became ready is left to process exit, as before: upstream components
	// cannot always be cancelled mid-startup (the apiserver exits 255 when a
	// post-start hook sees its context cancelled). Nothing depends on it, since
	// the services after it were never started, so only the ones before it are
	// stopped in order.
	stopStarted := func() {
		started = started[:len(started)-1]
		stopServices()
	}

	// infraServices must be fully ready before pod masquerade is set up.
	infraServices := []service{
		{
			name: "container runtime",
			start: func(ctx context.Context, cancel context.CancelFunc) {
				runtimeService := kubesoloruntime.NewService(ctx, cancel, runtimeReadyCh, &s.embedded)
				_ = runtimeService.Run()
			},
			readyCh: runtimeReadyCh,
		},
		{
			name: "kine",
			start: func(ctx context.Context, cancel context.CancelFunc) {
				kineService := kine.NewService(ctx, cancel, s.embedded.KineDir, kineReadyCh, s.cfg.Storage.DBWALRepair)
				_ = kineService.Run()
			},
			readyCh: kineReadyCh,
		},
		{
			name: "apiserver",
			start: func(ctx context.Context, cancel context.CancelFunc) {
				apiserverService := apiserver.NewService(ctx, cancel, apiServerReadyCh, s.embedded.NodeName, s.embedded)
				_ = apiserverService.Run(kineReadyCh)
			},
			readyCh: apiServerReadyCh,
		},
		{
			name: "controller",
			start: func(ctx context.Context, cancel context.CancelFunc) {
				controllerService := controller.NewService(ctx, cancel, controllerReadyCh, s.embedded.ControllerDir, s.embedded)
				_ = controllerService.Run(apiServerReadyCh)
			},
			readyCh: controllerReadyCh,
		},
	}

	// nodeServices start after masquerade is guaranteed to be in place.
	nodeServices := []service{
		{
			name: "kubelet",
			start: func(ctx context.Context, cancel context.CancelFunc) {
				kubeletService := kubelet.NewService(ctx, cancel, kubeletReadyCh, &s.embedded)
				_ = kubeletService.Run(apiServerReadyCh)
			},
			readyCh: kubeletReadyCh,
		},
		{
			name: "kubeproxy",
			start: func(ctx context.Context, cancel context.CancelFunc) {
				kubeproxyService := kubeproxy.NewService(ctx, cancel, kubeproxyReadyCh, s.embedded.AdminKubeconfigFile, s.embedded.ContainerMode)
				_ = kubeproxyService.Run(kubeletReadyCh)
			},
			readyCh: kubeproxyReadyCh,
		},
	}

	for _, svc := range infraServices {
		log.Info().Str("component", "kubesolo").Msgf("starting %s...", svc.name)
		startService(svc)
		if !waitForService(ctx, svc.name, svc.readyCh) {
			stopStarted()
			return
		}

		// Start the optional metrics endpoint as soon as kine is ready, so the
		// kine_db_size_bytes collector has a real path to stat. The metrics
		// service does not block any other component on its own readiness.
		if svc.name == "kine" && s.embedded.Metrics.Enabled {
			s.startMetricsService(ctx, cancel)
		}

		// The configuration API has no dependency on kine either; this is simply
		// the point at which the control plane is far enough along to be worth
		// exposing.
		if svc.name == "kine" && s.cfg.API.Enabled {
			s.startConfigAPIService(ctx, cancel)
		}
	}

	// Ensure pod→external masquerade (SNAT) is in place before kubelet starts.
	// kine persists cluster state across reboots, so kubelet will immediately
	// reconcile existing pods — they must not start into a network with no SNAT.
	log.Info().Str("component", "kubesolo").Msg("setting up pod masquerade rules...")
	if err := network.EnsurePodMasquerade(types.DefaultPodCIDR); err != nil {
		log.Fatal().Err(err).Msg("failed to set up pod masquerade")
	}

	for _, svc := range nodeServices {
		log.Info().Str("component", "kubesolo").Msgf("starting %s...", svc.name)
		startService(svc)
		if !waitForService(ctx, svc.name, svc.readyCh) {
			stopStarted()
			return
		}
	}

	log.Info().Str("component", "kubesolo").Msg("deploying coredns...")
	if err := coredns.Deploy(s.embedded.AdminKubeconfigFile, s.embedded.ContainerMode, s.embedded.DisableIPv6); err != nil {
		log.Fatal().Err(err).Msg("failed to deploy coredns")
	}

	if s.cfg.Storage.LocalPath.Enabled {
		log.Info().Str("component", "kubesolo").Msg("deploying local path...")
		if err := localpath.Deploy(s.embedded.AdminKubeconfigFile, s.embedded.LocalPathStorageDir, s.cfg.Storage.LocalPath.SharedPath); err != nil {
			log.Error().Err(err).Msg("failed to deploy local path, continuing without it")
		}
	}

	if s.cfg.Portainer.EdgeID != "" && s.cfg.Portainer.EdgeKey != "" {
		log.Info().Str("component", "kubesolo").Msg("deploying portainer edge agent...")
		if err := portainer.DeployEdgeAgent(s.embedded.AdminKubeconfigFile, types.EdgeAgentConfig{
			Image:            s.cfg.Portainer.Image,
			EdgeID:           s.cfg.Portainer.EdgeID,
			EdgeKey:          s.cfg.Portainer.EdgeKey,
			EdgeAsync:        s.cfg.Portainer.Async,
			EdgeInsecurePoll: "true",
			APISocketDir:     s.agentSocketDir(),
		}); err != nil {
			log.Error().Err(err).Msg("failed to deploy portainer edge agent, continuing without it")
		}
	}

	if s.cfg.D2K.Enabled {
		log.Info().Str("component", "kubesolo").Str("namespace", s.cfg.D2K.Namespace).Msg("deploying d2k...")
		if err := d2k.Deploy(s.embedded.AdminKubeconfigFile, d2k.Config{
			Namespace: s.cfg.D2K.Namespace,
			Image:     types.DefaultD2KImage,
			Certs:     s.embedded.D2KCerts,
		}); err != nil {
			log.Error().Err(err).Msg("failed to deploy d2k, continuing without it")
		}

	}

	s.wg.Go(func() { s.verifyPendingUpgrade(ctx) })

	<-ctx.Done()
	log.Info().Str("component", "kubesolo").Msg("shutting down...")

	stopServices()

	// Wait for all service goroutines to complete gracefully
	log.Info().Str("component", "kubesolo").Msg("waiting for all services to shutdown...")
	s.wg.Wait()
	log.Info().Str("component", "kubesolo").Msg("all services have shutdown gracefully")
}

// cleanStaleState removes the containerd artifacts that a new run regenerates:
// sockets, the generated config and the CNI directory.
//
// root/ is never removed. It holds the content store and snapshots, so deleting
// it destroys images side-loaded with `ctr images import`, which have no other
// source (issue #197).
//
// state/ holds one bundle per running task and is cleared only after a reboot.
// Within a boot those shims are still alive and containerd reattaches to them.
// Clearing it strands their container processes in kubepods.slice, where kubelet
// cannot see them, and a second copy of every pod starts alongside.
//
// containerd reconciles what is left: it deletes bundles it cannot reach and
// removes their work directories under root/.
//
// Nothing is cleaned when the container runtime is managed by the host: the socket
// and every directory below belong to that runtime, not to kubesolo.
func cleanStaleState(basePath string, runtimeExternal, containerMode bool) {
	if runtimeExternal {
		log.Debug().Str("component", "kubesolo").Msg("container runtime is managed by the host, leaving its state alone")
		return
	}

	// Stale system containerd socket symlink. Only a symlink is ever removed: that is
	// what kubesolo installs here, whereas a containerd managed by the host binds a
	// real socket at the same path.
	if filesystem.RemoveIfSymlink(types.DefaultSystemContainerdSock) {
		log.Info().Str("component", "kubesolo").Msgf("removed stale system containerd socket: %s", types.DefaultSystemContainerdSock)
	}

	// Must run before the directory read below, which returns early on a fresh
	// install and would leave the boot unrecorded until the second start.
	//
	// In container mode the boot id is the host's, so it survives the container
	// being replaced, which destroys every shim. That is a new boot.
	rebooted := rebootedSinceLastRun(basePath) || containerMode

	// Clean all containerd subdirectories except images/ (embedded tar archives)
	containerdDir := filepath.Join(basePath, types.DefaultContainerdDir)

	// The boot marker is not proof of a reboot. It is missing on the first start
	// after an upgrade from a release that never wrote one, and an unreadable
	// marker counts as a reboot too. A shim still attached to this containerd is
	// proof of the opposite: its containers are running, and clearing state/
	// would strand them while kubelet starts a second copy of every pod.
	if rebooted && !containerMode {
		if shims := liveShims("/proc", filepath.Join(containerdDir, types.DefaultContainerdSocket)); shims > 0 {
			log.Warn().Str("component", "kubesolo").Int("shims", shims).
				Msg("boot marker says this is a new boot, but containerd shims from the previous run are still alive; keeping containerd task state")
			rebooted = false
		}
	}
	entries, err := os.ReadDir(containerdDir)
	if err != nil {
		return
	}

	if !rebooted {
		log.Info().Str("component", "kubesolo").Msg("same boot as the previous run, keeping containerd task state")
	}

	for _, entry := range entries {
		name := entry.Name()
		// Preserve embedded image archives — they are re-imported on startup
		if name == "images" {
			continue
		}
		// Preserve embedded binaries and config template
		if name == "containerd" || name == "containerd-shim-runc-v2" || name == "crun" {
			continue
		}
		// Preserve registry
		if name == "registry" {
			continue
		}
		if name == "root" {
			continue
		}
		if name == "state" && !rebooted {
			continue
		}

		target := filepath.Join(containerdDir, name)
		if err := os.RemoveAll(target); err == nil {
			log.Info().Str("component", "kubesolo").Msgf("cleaned stale containerd artifact: %s", target)
		}
	}
}

// serviceStopTimeout bounds how long shutdown waits for each service to return,
// so stopping all of them stays well inside systemd's default 90s TimeoutStopSec.
const serviceStopTimeout = 10 * time.Second

const (
	bootIDPath       = "/proc/sys/kernel/random/boot_id"
	bootIDMarkerFile = ".boot-id"
)

// readBootID returns a value that changes every time the host boots. Empty when
// the kernel's boot_id cannot be read.
func readBootID() string {
	raw, err := os.ReadFile(bootIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// rebootedSinceLastRun reports whether the host has booted since the previous
// kubesolo start, and records the current boot for the next one. An
// unidentifiable boot, or a marker that cannot be read or written, counts as a
// reboot.
func rebootedSinceLastRun(basePath string) bool {
	current := readBootID()
	marker := filepath.Join(basePath, bootIDMarkerFile)

	if err := filesystem.EnsureDirectoryExists(basePath); err != nil {
		log.Warn().Str("component", "kubesolo").Msgf("could not create %s to record the boot marker: %v", basePath, err)
		return true
	}

	previous, readErr := os.ReadFile(marker)

	if current == "" {
		return true
	}

	if err := os.WriteFile(marker, []byte(current), 0o600); err != nil {
		log.Warn().Str("component", "kubesolo").Msgf("could not record the boot marker, treating this start as a reboot: %v", err)
		return true
	}

	if readErr != nil {
		return true
	}
	return strings.TrimSpace(string(previous)) != current
}

// verifyPendingUpgrade finishes what an upgrade executor that is no longer
// running left behind, once KubeSolo has been healthy for a while: it clears a
// pending verification of this version, and closes a run the executor never
// recorded the end of.
//
// Normally the executor does both. This covers it not being there — the host
// lost power or rebooted mid-upgrade — so that a version that came up fine is
// not later reverted by the boot guard for restarts that had nothing to do with
// it, and so that /api/v1/status does not report a run as forever unfinished.
func (s *kubesolo) verifyPendingUpgrade(ctx context.Context) {
	l := upgrade.NewLayout(s.cfg.Path, *flags.Config)

	raw, _ := os.ReadFile(l.PendingFile())
	pendingHere := strings.TrimSpace(string(raw)) == Version
	st, _ := upgrade.LoadState(l)
	interrupted := st != nil && st.Current != nil && !st.InFlight()
	if !pendingHere && !interrupted {
		return
	}
	if pendingHere {
		log.Info().Str("component", "upgrade").Str("version", Version).Msg("this version is pending verification after an upgrade")
	}

	const settle = time.Minute
	var healthySince time.Time
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		h := executor.CheckHealth(ctx, l.AdminKubeconfig(), Version)
		if !h.Healthy {
			healthySince = time.Time{}
			continue
		}
		if healthySince.IsZero() {
			healthySince = time.Now()
		}
		if time.Since(healthySince) < settle {
			continue
		}

		// An executor holding the lock is verifying this version itself.
		lock, err := upgrade.TryLock(l)
		if err != nil {
			continue
		}
		if raw, _ := os.ReadFile(l.PendingFile()); strings.TrimSpace(string(raw)) == Version {
			_ = os.Remove(l.PendingFile())
			_ = os.Remove(l.AttemptsFile())
			log.Info().Str("component", "upgrade").Str("version", Version).Msg("upgrade verified: KubeSolo is healthy")
		}
		if st, err := upgrade.LoadState(l); err == nil && st.CloseInterrupted(Version, lastGuardEvent(l)) {
			upgrade.SettleBackups(l, st.Last.Operation, st.Last.Result)
			_ = upgrade.SaveState(l, st)
			log.Info().Str("component", "upgrade").Str("result", string(st.Last.Result)).Msg("closed an upgrade run whose executor stopped before it finished")
		}
		lock.Unlock()
		return
	}
}

// lastGuardEvent is the boot guard's most recent record of a restore, if any.
func lastGuardEvent(l upgrade.Layout) string {
	raw, err := os.ReadFile(l.GuardLog())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	return lines[len(lines)-1]
}

// liveShims counts the containerd shims under procRoot that serve the containerd
// listening on socket. A shim names its containerd with -address, so a shim of a
// containerd managed by the host, or of another KubeSolo, is not counted.
func liveShims(procRoot, socket string) int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() || strings.Trim(entry.Name(), "0123456789") != "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if !strings.Contains(filepath.Base(args[0]), "containerd-shim") {
			continue
		}
		for i, arg := range args {
			if (arg == "-address" || arg == "--address") && i+1 < len(args) && args[i+1] == socket {
				count++
				break
			}
		}
	}
	return count
}

// startMetricsService starts the optional kubesolo metrics endpoint and
// passes in every component readiness channel so per-component up gauges
// can flip in real time as services come online. It does not block on its
// own readiness — the metrics endpoint failing must not stop kubesolo.
func (s *kubesolo) startMetricsService(ctx context.Context, cancel context.CancelFunc) {
	log.Info().
		Str("component", "kubesolo").
		Str("bind-address", s.embedded.Metrics.BindAddress).
		Msg("starting metrics endpoint...")

	metricsService := metrics.NewService(
		ctx,
		cancel,
		metricsReadyCh,
		s.embedded,
		metrics.BuildInfo{Version: Version, Commit: Commit, BuildDate: BuildDate},
		map[string]<-chan struct{}{
			metrics.ComponentRuntime:    runtimeReadyCh,
			metrics.ComponentKine:       kineReadyCh,
			metrics.ComponentAPIServer:  apiServerReadyCh,
			metrics.ComponentController: controllerReadyCh,
			metrics.ComponentKubelet:    kubeletReadyCh,
			metrics.ComponentKubeProxy:  kubeproxyReadyCh,
		},
	)
	s.wg.Go(func() {
		if err := metricsService.Run(); err != nil {
			log.Error().
				Str("component", "kubesolo").
				Err(err).
				Msg("metrics endpoint exited with error")
		}
	})
}

// startConfigAPIService starts the optional configuration API on its unix
// socket. Like the metrics endpoint it does not block startup: the API failing
// must not stop KubeSolo from running.
func (s *kubesolo) startConfigAPIService(ctx context.Context, cancel context.CancelFunc) {
	log.Info().
		Str("component", "kubesolo").
		Str("socket", s.cfg.API.SocketPath).
		Msg("starting configuration API...")

	kubeconfig := s.embedded.AdminKubeconfigFile
	configAPIService := configapi.NewService(ctx, cancel, configAPIReadyCh, configapi.Options{
		SocketPath:      s.cfg.API.SocketPath,
		AgentSocketPath: s.agentSocketPath(),
		ConfigPath:      *flags.Config,
		Host: config.Host{
			NumCPU:        runtime.NumCPU(),
			GOARCH:        runtime.GOARCH,
			ContainerMode: s.embedded.ContainerMode,
		},
		Lifecycle: &configapi.Lifecycle{
			Version:       Version,
			Commit:        Commit,
			DataDir:       s.cfg.Path,
			ContainerMode: s.embedded.ContainerMode,
			Spawn:         spawnUpgradeExecutor,
			Health: func(ctx context.Context) upgrade.Health {
				return executor.CheckHealth(ctx, kubeconfig, "")
			},
			AgentImage: func(ctx context.Context) (string, error) {
				return portainer.RunningAgentImage(ctx, kubeconfig)
			},
		},
	})
	s.wg.Go(func() {
		if err := configAPIService.Run(); err != nil {
			log.Error().
				Str("component", "kubesolo").
				Err(err).
				Msg("configuration API exited with error")
		}
	})
}

// agentSocketPath is the API socket mounted into the Portainer agent's pod, or
// "" when the agent gets none: it needs both the API enabled and an agent to
// give it to.
//
// This hands the agent upgrade, rollback and configuration control of the
// host. The agent already runs as cluster-admin, so it is little more than it
// can do anyway, but it is the reason the API must be enabled explicitly.
func (s *kubesolo) agentSocketPath() string {
	if !s.cfg.API.Enabled || s.cfg.Portainer.EdgeID == "" || s.cfg.Portainer.EdgeKey == "" {
		return ""
	}
	return filepath.Join(s.cfg.Path, agentSocketDirName, portainer.AgentSocketName)
}

// agentSocketDir is the directory mounted into the agent's pod, or "".
func (s *kubesolo) agentSocketDir() string {
	if p := s.agentSocketPath(); p != "" {
		return filepath.Dir(p)
	}
	return ""
}

// agentSocketDirName holds only the agent's API socket, so that it can be mounted
// into a pod without the rest of the data directory.
const agentSocketDirName = "agent-api"

// spawnUpgradeExecutor starts this binary as the upgrade executor, outside the
// KubeSolo service.
func spawnUpgradeExecutor(jobFile string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	info, err := detect.Detect()
	if err != nil {
		return err
	}
	return upgrade.SpawnExecutor(exe, jobFile, info.InitSystem)
}

// waitForService waits for a service to be ready
// it returns true if the service is ready
// it returns false if the service is not ready and the shutdown signal has been received
func waitForService(ctx context.Context, name string, readyCh chan struct{}) bool {
	select {
	case <-readyCh:
		log.Info().Str("component", "kubesolo").Msgf("%s is ready...", name)
		return true
	case <-ctx.Done():
		log.Info().Str("component", "kubesolo").Msgf("shutdown requested before %s was ready...", name)
		return false
	}
}

// bootstrap is the bootstrap function for the kubesolo application
// it sets up the logging, pprof server, and garbage collection
// it also sets up all required paths for the application
func (s *kubesolo) bootstrap() {
	if s.cfg.Logging.Debug {
		log.Info().Msg("debug mode enabled")
		logging.SetLoggingLevel("DEBUG")
	}

	if s.cfg.Logging.Pprof {
		system.StartMonitoring()
	}

	// Setup logging
	logging.ConfigureLogger()
	logging.SetLoggingMode("PRETTY")
	logging.SetLoggingLevel("INFO")
	logging.ConfigureK8sDefaultLogging()
	logging.ConfigureLogrusLogging()

	// Load required kernel modules before any networking setup
	system.LoadRequiredModules()

	if s.cfg.Network.DisableIPv6 {
		if err := network.DisableIPv6Sysctls(); err != nil {
			log.Warn().Err(err).Msg("failed to disable ipv6 sysctls")
		}
	}

	// System Node IP
	nodeIP, nodeIPPinned, err := network.ResolveNodeIP(s.cfg.Network.NodeIP)
	if err != nil {
		log.Warn().Err(err).Msg("failed to get node IP address, using default loopback IP address")
	}

	// LoadBalancer EXTERNAL-IP, which may differ from the node IP on multi-NIC hosts
	loadBalancerIP := network.ResolveLoadBalancerIP(s.cfg.Network.LoadBalancer.IP, nodeIP)

	// Network MTU
	mtu, mtuPinned, err := network.ResolveMTU(s.cfg.Network.MTU)
	if err != nil {
		log.Warn().Err(err).Msg("failed to detect network MTU, using default MTU")
	}
	// config.Validate already raises this for a configured MTU, so only the
	// auto-detected case is left to report here — otherwise a user who
	// configured a low MTU would be told about it twice.
	if !mtuPinned && mtu < 1280 && !s.cfg.Network.DisableIPv6 {
		log.Warn().Int("mtu", mtu).Msg("MTU is below the IPv6 minimum (1280); IPv6 pod traffic may fail to fragment correctly")
	}

	// Disable OpenTelemetry SDK to prevent it from interfering with the application's logging
	_ = os.Setenv("OTEL_SDK_DISABLED", "true")

	// Setup paths
	basePath := s.cfg.Path
	containerMode := config.ResolveContainerMode(s.cfg, system.IsRunningInContainer())
	if containerMode {
		log.Info().Str("component", "kubesolo").Msg("container mode detected, using cgroupfs driver and relaxed eviction thresholds")

		if err := system.SetupContainerMounts(); err != nil {
			log.Fatal().Err(err).Msg("failed to setup container mount propagation")
		}

		if err := system.SetupContainerCgroups(); err != nil {
			log.Fatal().Err(err).Msg("failed to setup container cgroups")
		}
	}

	// Clean stale runtime state from previous runs (e.g., after reboot)
	// This removes stale sockets and containerd runtime state that reference
	// dead processes, while preserving images, kine database, and PKI certs.
	//
	// s.runtimeEndpoint.External is used rather than the endpoint BuildEmbedded
	// resolves: substituting the embedded endpoint never changes External, since
	// cri.Embedded leaves it false.
	cleanStaleState(basePath, s.runtimeEndpoint.External, containerMode)

	s.embedded = config.BuildEmbedded(s.cfg, config.Probe{
		NodeIP:          nodeIP,
		NodeIPPinned:    nodeIPPinned,
		LoadBalancerIP:  loadBalancerIP,
		MTU:             mtu,
		MTUPinned:       mtuPinned,
		ContainerMode:   containerMode,
		RuntimeEndpoint: s.runtimeEndpoint,
		Hostname:        s.hostName,
	})
}
