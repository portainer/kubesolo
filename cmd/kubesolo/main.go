package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"github.com/alecthomas/kingpin/v2"
	"github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/internal/config/flags"
	"github.com/portainer/kubesolo/internal/core/embedded"
	"github.com/portainer/kubesolo/internal/core/pki"
	"github.com/portainer/kubesolo/internal/logging"
	"github.com/portainer/kubesolo/internal/runtime/cri"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/pkg/components/configapi"
	"github.com/portainer/kubesolo/pkg/components/coredns"
	"github.com/portainer/kubesolo/pkg/components/d2k"
	"github.com/portainer/kubesolo/pkg/components/localpath"
	"github.com/portainer/kubesolo/pkg/components/metrics"
	"github.com/portainer/kubesolo/pkg/components/portainer"
	"github.com/portainer/kubesolo/pkg/kine"
	"github.com/portainer/kubesolo/pkg/kubernetes/apiserver"
	"github.com/portainer/kubesolo/pkg/kubernetes/bootstrap"
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
		start   func()
		readyCh chan struct{}
	}

	// infraServices must be fully ready before pod masquerade is set up.
	infraServices := []service{
		{
			name: "container runtime",
			start: func() {
				runtimeService := kubesoloruntime.NewService(ctx, cancel, runtimeReadyCh, &s.embedded)
				s.wg.Go(func() {
					_ = runtimeService.Run()
				})
			},
			readyCh: runtimeReadyCh,
		},
		{
			name: "datastore",
			start: func() {
				// With a host-managed etcd there is no datastore for KubeSolo to
				// run: the API server was already pointed at it, so the only thing
				// left is to release the components waiting on this channel.
				if s.embedded.EtcdExternal {
					log.Info().Str("component", "kubesolo").
						Strs("endpoints", s.embedded.EtcdEndpoints).
						Msg("using the etcd managed by the host, not starting kine")
					close(kineReadyCh)
					return
				}

				kineService := kine.NewService(ctx, cancel, s.embedded.KineDir, kineReadyCh, s.cfg.Storage.DBWALRepair)
				s.wg.Go(func() {
					_ = kineService.Run()
				})
			},
			readyCh: kineReadyCh,
		},
		{
			name: "apiserver",
			start: func() {
				apiserverService := apiserver.NewService(ctx, cancel, apiServerReadyCh, s.embedded.NodeName, s.embedded)
				s.wg.Go(func() {
					_ = apiserverService.Run(kineReadyCh)
				})
			},
			readyCh: apiServerReadyCh,
		},
		{
			name: "controller",
			start: func() {
				controllerService := controller.NewService(ctx, cancel, controllerReadyCh, s.embedded.ControllerDir, s.embedded)
				s.wg.Go(func() {
					_ = controllerService.Run(apiServerReadyCh)
				})
			},
			readyCh: controllerReadyCh,
		},
	}

	// nodeServices start after masquerade is guaranteed to be in place.
	nodeServices := []service{
		{
			name: "kubelet",
			start: func() {
				kubeletService := kubelet.NewService(ctx, cancel, kubeletReadyCh, &s.embedded)
				s.wg.Go(func() {
					_ = kubeletService.Run(apiServerReadyCh)
				})
			},
			readyCh: kubeletReadyCh,
		},
		{
			name: "kubeproxy",
			start: func() {
				kubeproxyService := kubeproxy.NewService(ctx, cancel, kubeproxyReadyCh, s.embedded.AdminKubeconfigFile, s.embedded.ContainerMode)
				s.wg.Go(func() {
					_ = kubeproxyService.Run(kubeletReadyCh)
				})
			},
			readyCh: kubeproxyReadyCh,
		},
	}

	for _, svc := range infraServices {
		log.Info().Str("component", "kubesolo").Msgf("starting %s...", svc.name)
		svc.start()
		if !waitForService(ctx, svc.name, svc.readyCh) {
			return
		}

		// Start the optional metrics endpoint as soon as kine is ready, so the
		// kine_db_size_bytes collector has a real path to stat. The metrics
		// service does not block any other component on its own readiness.
		if svc.name == "datastore" && s.embedded.Metrics.Enabled {
			s.startMetricsService(ctx, cancel)
		}

		// The configuration API has no dependency on kine either; this is simply
		// the point at which the control plane is far enough along to be worth
		// exposing.
		if svc.name == "datastore" && s.cfg.API.Enabled {
			s.startConfigAPIService(ctx, cancel)
		}
	}

	// TLS bootstrapping is seeded before any kubelet is expected, so that a
	// foreign kubelet already retrying against the API server finds the token
	// valid on its next attempt rather than after a further backoff.
	if s.cfg.Kubernetes.BootstrapToken != "" {
		log.Info().Str("component", "kubesolo").Msg("enabling tls bootstrapping...")
		if err := bootstrap.Apply(s.embedded.AdminKubeconfigFile, s.cfg.Kubernetes.BootstrapToken); err != nil {
			log.Fatal().Err(err).Msg("failed to enable tls bootstrapping")
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
		svc.start()
		if !waitForService(ctx, svc.name, svc.readyCh) {
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

	<-sigCh
	log.Info().Str("component", "kubesolo").Msg("shutting down...")

	// Wait for all service goroutines to complete gracefully
	log.Info().Str("component", "kubesolo").Msg("waiting for all services to shutdown...")
	s.wg.Wait()
	log.Info().Str("component", "kubesolo").Msg("all services have shutdown gracefully")
}

// cleanStaleState removes stale runtime artifacts from a previous run.
// After a reboot, the old container is gone but stale containerd metadata,
// sockets, and runtime state remain on the persistent volume. The containerd
// metadata DB (meta.db) retains references to EXITED containers, causing
// kubelet to fail pod synchronization on restart.
//
// Strategy: remove everything in the containerd directory except the embedded
// image archives (images/). These are re-imported by importImages() on every
// startup, so no data is lost. This gives containerd a clean slate while
// preserving the kine database (Kubernetes state) and PKI certificates.
//
// Nothing is cleaned when the container runtime is managed by the host: the socket
// and every directory below belong to that runtime, not to kubesolo.
func cleanStaleState(basePath string, runtimeExternal bool) {
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

	// Clean all containerd subdirectories except images/ (embedded tar archives)
	containerdDir := filepath.Join(basePath, types.DefaultContainerdDir)
	entries, err := os.ReadDir(containerdDir)
	if err != nil {
		return
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

		target := filepath.Join(containerdDir, name)
		if err := os.RemoveAll(target); err == nil {
			log.Info().Str("component", "kubesolo").Msgf("cleaned stale containerd artifact: %s", target)
		}
	}
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

	configAPIService := configapi.NewService(ctx, cancel, configAPIReadyCh, configapi.Options{
		SocketPath: s.cfg.API.SocketPath,
		ConfigPath: *flags.Config,
		Host: config.Host{
			NumCPU:        runtime.NumCPU(),
			GOARCH:        runtime.GOARCH,
			ContainerMode: s.embedded.ContainerMode,
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
	cleanStaleState(basePath, s.runtimeEndpoint.External)

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
