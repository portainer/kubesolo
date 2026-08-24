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

	"github.com/alecthomas/kingpin/v2"
	"github.com/distribution/reference"
	"github.com/portainer/kubesolo/internal/config/cpumanager"
	"github.com/portainer/kubesolo/internal/config/flags"
	"github.com/portainer/kubesolo/internal/core/embedded"
	"github.com/portainer/kubesolo/internal/core/pki"
	"github.com/portainer/kubesolo/internal/logging"
	"github.com/portainer/kubesolo/internal/runtime/cri"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/internal/system"
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
)

var (
	Version   = "dev"
	BuildDate = "unknown"
	Commit    = "unknown"
)

// the main struct for the kubesolo application
type kubesolo struct {
	wg                     sync.WaitGroup
	hostName               string
	extraSANs              string
	debug                  bool
	pprofServer            bool
	portainerEdgeID        string
	portainerEdgeKey       string
	portainerEdgeAsync     bool
	portainerEdgeImage     string
	loadBalancer           bool
	localStorage           bool
	localStorageSharedPath string
	disableIPv6            bool
	dbWALRepair            bool
	d2k                    bool
	d2kNamespace           string
	runtimeEndpoint        cri.Endpoint
	metricsServer          bool
	metricsBindAddress     string
	cpuManager             types.CPUManagerConfig
	systemReserved         map[string]string
	embedded               types.Embedded
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
)

// service creates a new kubesolo application
func service() (*kubesolo, error) {
	d2kEnabled := *flags.D2K
	if d2kEnabled && (runtime.GOARCH == "arm" || runtime.GOARCH == "riscv64") {
		log.Warn().Str("component", "kubesolo").Str("arch", runtime.GOARCH).Msg("d2k is not supported on this architecture, disabling")
		d2kEnabled = false
	}
	if d2kEnabled && !*flags.LoadBalancer {
		log.Fatal().Str("component", "kubesolo").Msg("--d2k requires --load-balancer: the d2k Service endpoint is populated by the LoadBalancer webhook")
	}

	portainerEdgeImage, err := normaliseImageRef(*flags.PortainerEdgeImage)
	if err != nil {
		return nil, err
	}

	runtimeEndpoint, err := cri.Resolve(*flags.ContainerRuntimeEndpoint)
	if err != nil {
		return nil, err
	}

	cpuManagerConfig, systemReserved, err := cpumanager.Parse(*flags.CPUManagerPolicy, *flags.CPUManagerPolicyOptions, *flags.ReservedCPUs, *flags.SystemReserved, runtime.NumCPU())
	if err != nil {
		return nil, err
	}

	return &kubesolo{
		hostName:               system.GetHostname(),
		extraSANs:              *flags.APIServerExtraSANs,
		debug:                  *flags.Debug,
		pprofServer:            *flags.PprofServer,
		portainerEdgeID:        *flags.PortainerEdgeID,
		portainerEdgeKey:       *flags.PortainerEdgeKey,
		portainerEdgeAsync:     *flags.PortainerEdgeAsync,
		portainerEdgeImage:     portainerEdgeImage,
		loadBalancer:           *flags.LoadBalancer,
		localStorage:           *flags.LocalStorage,
		localStorageSharedPath: *flags.LocalStorageSharedPath,
		disableIPv6:            *flags.DisableIPv6,
		dbWALRepair:            *flags.DBWALRepair,
		d2k:                    d2kEnabled,
		d2kNamespace:           *flags.D2KNamespace,
		runtimeEndpoint:        runtimeEndpoint,
		metricsServer:          *flags.MetricsServer,
		metricsBindAddress:     *flags.MetricsBindAddress,
		cpuManager:             cpuManagerConfig,
		systemReserved:         systemReserved,
	}, nil
}

// normaliseImageRef expands a short image reference such as portainerci/agent:develop
// into a fully qualified one (docker.io/portainerci/agent:develop). The containerd
// client, unlike the Docker CLI, applies no Docker Hub defaults and would otherwise
// treat the first component as a registry host and fail to resolve it.
func normaliseImageRef(image string) (string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %v", image, err)
	}

	return reference.TagNameOnly(named).String(), nil
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

	if *flags.StartupTimeout > 0 {
		types.DefaultRetryCount = *flags.StartupTimeout / int(types.DefaultComponentSleep.Seconds())
	}

	service, err := service()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create service. check the logs for more information. exiting...")
	}

	service.bootstrap()
	service.run()
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
	if err := pki.InvalidateIfIPChanged(s.embedded); err != nil {
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
			name: "kine",
			start: func() {
				kineService := kine.NewService(ctx, cancel, s.embedded.KineDir, kineReadyCh, s.dbWALRepair)
				s.wg.Go(func() {
					_ = kineService.Run()
				})
			},
			readyCh: kineReadyCh,
		},
		{
			name: "apiserver",
			start: func() {
				apiserverService := apiserver.NewService(ctx, cancel, apiServerReadyCh, s.hostName, s.embedded)
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
		if svc.name == "kine" && s.embedded.Metrics.Enabled {
			s.startMetricsService(ctx, cancel)
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

	if s.localStorage {
		log.Info().Str("component", "kubesolo").Msg("deploying local path...")
		if err := localpath.Deploy(s.embedded.AdminKubeconfigFile, s.embedded.LocalPathStorageDir, s.localStorageSharedPath); err != nil {
			log.Error().Err(err).Msg("failed to deploy local path, continuing without it")
		}
	}

	if s.portainerEdgeID != "" && s.portainerEdgeKey != "" {
		log.Info().Str("component", "kubesolo").Msg("deploying portainer edge agent...")
		if err := portainer.DeployEdgeAgent(s.embedded.AdminKubeconfigFile, types.EdgeAgentConfig{
			Image:            s.portainerEdgeImage,
			EdgeID:           s.portainerEdgeID,
			EdgeKey:          s.portainerEdgeKey,
			EdgeAsync:        s.portainerEdgeAsync,
			EdgeInsecurePoll: "true",
		}); err != nil {
			log.Error().Err(err).Msg("failed to deploy portainer edge agent, continuing without it")
		}
	}

	if s.d2k {
		log.Info().Str("component", "kubesolo").Str("namespace", s.d2kNamespace).Msg("deploying d2k...")
		if err := d2k.Deploy(s.embedded.AdminKubeconfigFile, d2k.Config{
			Namespace: s.d2kNamespace,
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
	if s.debug {
		log.Info().Msg("debug mode enabled")
		logging.SetLoggingLevel("DEBUG")
	}

	if s.pprofServer {
		system.StartMonitoring()
	}

	// Setup logging
	logging.ConfigureLogger()
	logging.SetLoggingMode("PRETTY")
	logging.SetLoggingLevel("INFO")
	logging.ConfigureK8sDefaultLogging()

	// Load required kernel modules before any networking setup
	system.LoadRequiredModules()

	if s.disableIPv6 {
		if err := network.DisableIPv6Sysctls(); err != nil {
			log.Warn().Err(err).Msg("failed to disable ipv6 sysctls")
		}
	}

	// System Node IP
	nodeIP, nodeIPPinned, err := network.ResolveNodeIP(*flags.NodeIP)
	if err != nil {
		log.Warn().Err(err).Msg("failed to get node IP address, using default loopback IP address")
	}

	// LoadBalancer EXTERNAL-IP, which may differ from the node IP on multi-NIC hosts
	loadBalancerIP := network.ResolveLoadBalancerIP(*flags.LoadBalancerIP, nodeIP)

	// Network MTU
	mtu, mtuPinned, err := network.ResolveMTU(*flags.MTU)
	if err != nil {
		log.Warn().Err(err).Msg("failed to detect network MTU, using default MTU")
	}
	if mtu < 1280 && !s.disableIPv6 {
		log.Warn().Int("mtu", mtu).Msg("MTU is below the IPv6 minimum (1280); IPv6 pod traffic may fail to fragment correctly")
	}

	// Disable OpenTelemetry SDK to prevent it from interfering with the application's logging
	_ = os.Setenv("OTEL_SDK_DISABLED", "true")

	// Setup paths
	basePath := *flags.Path
	containerMode := *flags.ContainerMode || system.IsRunningInContainer()
	if containerMode && s.cpuManager.Policy == types.CPUManagerPolicyStatic {
		log.Fatal().Str("component", "kubesolo").Msg("--cpu-manager-policy=static is not supported in container mode: exclusive cores are bounded by the container's own cpuset, which kubesolo does not control")
	}
	if containerMode {
		log.Info().Str("component", "kubesolo").Msg("container mode detected, using cgroupfs driver and relaxed eviction thresholds")

		if err := system.SetupContainerMounts(); err != nil {
			log.Fatal().Err(err).Msg("failed to setup container mount propagation")
		}

		if err := system.SetupContainerCgroups(); err != nil {
			log.Fatal().Err(err).Msg("failed to setup container cgroups")
		}
	}

	// Container runtime endpoint. Without --container-runtime-endpoint KubeSolo
	// starts its own containerd and talks to it over the socket under --path.
	containerdSocketFile := filepath.Join(basePath, types.DefaultContainerdDir, types.DefaultContainerdSocket)
	runtimeEndpoint := s.runtimeEndpoint
	if !runtimeEndpoint.External {
		runtimeEndpoint = cri.Embedded(containerdSocketFile)
	}

	// Clean stale runtime state from previous runs (e.g., after reboot)
	// This removes stale sockets and containerd runtime state that reference
	// dead processes, while preserving images, kine database, and PKI certs.
	cleanStaleState(basePath, runtimeEndpoint.External)

	s.embedded = types.Embedded{
		// System Node IP
		NodeIP:          nodeIP,
		NodeIPSpecified: nodeIPPinned,

		// Network MTU
		MTU:          mtu,
		MTUSpecified: mtuPinned,

		// Admin kubeconfig file
		AdminKubeconfigFile: filepath.Join(basePath, types.DefaultPKIDir, "admin", "admin.kubeconfig"),

		// PKI paths
		PKIDir:              filepath.Join(basePath, types.DefaultPKIDir),
		PKICADir:            filepath.Join(basePath, types.DefaultPKIDir, "ca"),
		PKIAdminDir:         filepath.Join(basePath, types.DefaultPKIDir, "admin"),
		PKIAPIServerDir:     filepath.Join(basePath, types.DefaultPKIDir, "apiserver"),
		PKIControllerDir:    filepath.Join(basePath, types.DefaultPKIDir, "controller-manager"),
		PKIKubeletDir:       filepath.Join(basePath, types.DefaultPKIDir, "kubelet"),
		PKIWebhookDir:       filepath.Join(basePath, types.DefaultPKIDir, "webhook"),
		PKIRequestHeaderDir: filepath.Join(basePath, types.DefaultPKIDir, "request-header"),

		// Certificate paths
		KubeletCerts: types.KubeletCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.crt"),
				Cert:   filepath.Join(basePath, types.DefaultPKIDir, "kubelet", "kubelet.crt"),
				Key:    filepath.Join(basePath, types.DefaultPKIDir, "kubelet", "kubelet.key"),
			},
		},
		APIServerCerts: types.APIServerCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.crt"),
				Cert:   filepath.Join(basePath, types.DefaultPKIDir, "apiserver", "apiserver.crt"),
				Key:    filepath.Join(basePath, types.DefaultPKIDir, "apiserver", "apiserver.key"),
			},
		},
		ControllerManagerCerts: types.ControllerManagerCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.crt"),
				Cert:   filepath.Join(basePath, types.DefaultPKIDir, "controller-manager", "controller-manager.crt"),
				Key:    filepath.Join(basePath, types.DefaultPKIDir, "controller-manager", "controller-manager.key"),
			},
		},
		AdminCerts: types.AdminCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.crt"),
				Cert:   filepath.Join(basePath, types.DefaultPKIDir, "admin", "admin.crt"),
				Key:    filepath.Join(basePath, types.DefaultPKIDir, "admin", "admin.key"),
			},
		},
		WebhookCerts: types.WebhookCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.crt"),
				Cert:   filepath.Join(basePath, types.DefaultPKIDir, "webhook", "webhook.crt"),
				Key:    filepath.Join(basePath, types.DefaultPKIDir, "webhook", "webhook.key"),
			},
		},
		CACerts: types.CACertificatePaths{
			Cert: filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.crt"),
			Key:  filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.key"),
		},
		RequestHeaderCerts: types.RequestHeaderCertificatePaths{
			CACert:     filepath.Join(basePath, types.DefaultPKIDir, "request-header", "request-header-ca.crt"),
			CAKey:      filepath.Join(basePath, types.DefaultPKIDir, "request-header", "request-header-ca.key"),
			ClientCert: filepath.Join(basePath, types.DefaultPKIDir, "request-header", "request-header-client.crt"),
			ClientKey:  filepath.Join(basePath, types.DefaultPKIDir, "request-header", "request-header-client.key"),
		},

		// Containerd paths
		ContainerdDir:               filepath.Join(basePath, types.DefaultContainerdDir),
		ContainerdSocketFile:        containerdSocketFile,
		ContainerdBinaryFile:        filepath.Join(basePath, types.DefaultContainerdDir, "containerd"),
		ContainerdImagesDir:         filepath.Join(basePath, types.DefaultContainerdDir, "images"),
		ContainerdShimBinaryFile:    filepath.Join(basePath, types.DefaultContainerdDir, "containerd-shim-runc-v2"),
		ContainerdConfigFile:        filepath.Join(basePath, types.DefaultContainerdDir, "config.toml"),
		ContainerdRootDir:           filepath.Join(basePath, types.DefaultContainerdDir, "root"),
		ContainerdStateDir:          filepath.Join(basePath, types.DefaultContainerdDir, "state"),
		ContainerdRegistryConfigDir: filepath.Join(basePath, types.DefaultContainerdDir, "registry"),

		// CNI paths
		ContainerdCNIDir:        filepath.Join(basePath, types.DefaultContainerdDir, "cni"),
		ContainerdCNIPluginsDir: filepath.Join(basePath, types.DefaultContainerdDir, "cni", "plugins"),
		ContainerdCNIConfigDir:  filepath.Join(basePath, types.DefaultContainerdDir, "cni", "conf"),
		ContainerdCNIConfigFile: filepath.Join(basePath, types.DefaultContainerdDir, "cni", "conf", types.DefaultCNIConfigName),

		// Crun binary
		CrunBinaryFile: filepath.Join(basePath, types.DefaultContainerdDir, "crun"),

		// Container runtime
		RuntimeExternal:   runtimeEndpoint.External,
		RuntimeEndpoint:   runtimeEndpoint.URL,
		RuntimeSocketPath: runtimeEndpoint.SocketPath,

		// Kubelet paths
		KubeletDir:            filepath.Join(basePath, types.DefaultKubeletDir),
		KubeletConfigDir:      filepath.Join(basePath, types.DefaultKubeletDir, "config"),
		KubeletConfigFile:     filepath.Join(basePath, types.DefaultKubeletDir, "config", "config.yaml"),
		KubeletKubeConfigFile: filepath.Join(basePath, types.DefaultPKIDir, "kubelet", "kubelet.kubeconfig"),
		KubeletPluginsDir:     filepath.Join(basePath, types.DefaultKubeletDir, "volumeplugins"),

		// API Server paths
		APIServerDir:          filepath.Join(basePath, types.DefaultAPIServerDir),
		ServiceAccountKeyFile: filepath.Join(basePath, types.DefaultPKIDir, "apiserver", "service-account.key"),
		// API Server extra SANs
		APIServerExtraSANs: strings.Split(s.extraSANs, ","),

		// Kine paths
		KineDir:        filepath.Join(basePath, types.KubesoloKineDir),
		KineSocketFile: filepath.Join(basePath, types.KubesoloKineDir, "socket"),

		// Controller manager paths
		ControllerDir: filepath.Join(basePath, types.KubesoloControllerManagerDir),

		// Webhook paths
		WebhookDir: filepath.Join(basePath, types.KubesoloWebhookDir),

		// Image paths
		PortainerEdgeImageFile:        filepath.Join(basePath, types.DefaultContainerdDir, "images", "portainer-agent.tar.gz"),
		CorednsImageFile:              filepath.Join(basePath, types.DefaultContainerdDir, "images", "coredns.tar.gz"),
		SandboxImageFile:              filepath.Join(basePath, types.DefaultContainerdDir, "images", "pause.tar.gz"),
		LocalPathProvisionerImageFile: filepath.Join(basePath, types.DefaultContainerdDir, "images", "local-path-provisioner.tar.gz"),

		// Load Balancer
		LoadBalancer:   s.loadBalancer,
		LoadBalancerIP: loadBalancerIP,

		// Local Path Storage
		LocalPathStorageDir: filepath.Join(basePath, types.DefaultLocalPathStorageDir),

		// Portainer Edge
		IsPortainerEdge:    s.portainerEdgeID != "" && s.portainerEdgeKey != "",
		PortainerEdgeImage: s.portainerEdgeImage,

		// Container Mode
		ContainerMode: containerMode,

		// IPv6
		DisableIPv6: s.disableIPv6,

		// d2k integration
		D2K:          s.d2k,
		D2KNamespace: s.d2kNamespace,
		D2KCerts: types.D2KCertificatePaths{
			CACert:     filepath.Join(basePath, types.DefaultPKIDir, "ca", "ca.crt"),
			ServerCert: filepath.Join(basePath, types.DefaultPKIDir, types.DefaultD2KDir, "server.crt"),
			ServerKey:  filepath.Join(basePath, types.DefaultPKIDir, types.DefaultD2KDir, "server.key"),
			ClientCert: filepath.Join(basePath, types.DefaultPKIDir, types.DefaultD2KDir, "client.crt"),
			ClientKey:  filepath.Join(basePath, types.DefaultPKIDir, types.DefaultD2KDir, "client.key"),
		},
		D2KImageFile: filepath.Join(basePath, types.DefaultContainerdDir, "images", "d2k.tar.gz"),

		// CPU manager
		CPUManager:     s.cpuManager,
		SystemReserved: s.systemReserved,

		// Metrics endpoint
		Metrics: types.MetricsConfig{
			Enabled:     s.metricsServer,
			BindAddress: s.metricsBindAddress,
		},
	}
}
