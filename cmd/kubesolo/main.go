package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/alecthomas/kingpin/v2"
	"github.com/portainer/kubesolo/internal/config/flags"
	"github.com/portainer/kubesolo/internal/core/embedded"
	"github.com/portainer/kubesolo/internal/core/pki"
	"github.com/portainer/kubesolo/internal/logging"
	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/pkg/components/coredns"
	"github.com/portainer/kubesolo/pkg/components/localpath"
	"github.com/portainer/kubesolo/pkg/components/portainer"
	"github.com/portainer/kubesolo/pkg/kine"
	"github.com/portainer/kubesolo/pkg/kubernetes/apiserver"
	"github.com/portainer/kubesolo/pkg/kubernetes/controller"
	"github.com/portainer/kubesolo/pkg/kubernetes/kubelet"
	"github.com/portainer/kubesolo/pkg/kubernetes/kubeproxy"
	"github.com/portainer/kubesolo/pkg/runtime/containerd"
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
	loadBalancer           bool
	localStorage           bool
	localStorageSharedPath string
	embedded               types.Embedded
}

// the channels for the kubesolo application
var (
	containerdReadyCh = make(chan struct{})
	kineReadyCh       = make(chan struct{})
	apiServerReadyCh  = make(chan struct{})
	kubeletReadyCh    = make(chan struct{})
	controllerReadyCh = make(chan struct{})
	kubeproxyReadyCh  = make(chan struct{})
)

// service creates a new kubesolo application
func service() (*kubesolo, error) {
	return &kubesolo{
		hostName:               system.GetHostname(),
		extraSANs:              *flags.APIServerExtraSANs,
		debug:                  *flags.Debug,
		pprofServer:            *flags.PprofServer,
		portainerEdgeID:        *flags.PortainerEdgeID,
		portainerEdgeKey:       *flags.PortainerEdgeKey,
		portainerEdgeAsync:     *flags.PortainerEdgeAsync,
		loadBalancer:           *flags.LoadBalancer,
		localStorage:           *flags.LocalStorage,
		localStorageSharedPath: *flags.LocalStorageSharedPath,
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

	log.Info().Str("component", "kubesolo").Msg("generating relevant certificates...")
	if err := pki.GenerateAllCertificates(s.embedded); err != nil {
		log.Fatal().Err(err).Msg("failed to generate full certificates")
	}
	log.Info().Str("component", "kubesolo").Msg("starting kubesolo services... this may take a few minutes...")

	services := []struct {
		name    string
		start   func()
		readyCh chan struct{}
	}{
		{
			name: "containerd",
			start: func() {
				containerdService := containerd.NewService(ctx, cancel, containerdReadyCh, &s.embedded)
				s.wg.Go(func() {
					containerdService.Run()
				})
			},
			readyCh: containerdReadyCh,
		},
		{
			name: "kine",
			start: func() {
				kineService := kine.NewService(ctx, cancel, s.embedded.KineDir, kineReadyCh)
				s.wg.Go(func() {
					kineService.Run()
				})
			},
			readyCh: kineReadyCh,
		},
		{
			name: "apiserver",
			start: func() {
				apiserverService := apiserver.NewService(ctx, cancel, apiServerReadyCh, s.hostName, s.embedded)
				s.wg.Go(func() {
					apiserverService.Run(kineReadyCh)
				})
			},
			readyCh: apiServerReadyCh,
		},
		{
			name: "controller",
			start: func() {
				controllerService := controller.NewService(ctx, cancel, controllerReadyCh, s.embedded.ControllerDir, s.embedded)
				s.wg.Go(func() {
					controllerService.Run(apiServerReadyCh)
				})
			},
			readyCh: controllerReadyCh,
		},
		{
			name: "kubelet",
			start: func() {
				kubeletService := kubelet.NewService(ctx, cancel, kubeletReadyCh, &s.embedded)
				s.wg.Go(func() {
					kubeletService.Run(apiServerReadyCh)
				})
			},
			readyCh: kubeletReadyCh,
		},
		{
			name: "kubeproxy",
			start: func() {
				kubeproxyService := kubeproxy.NewService(ctx, cancel, kubeproxyReadyCh, s.embedded.AdminKubeconfigFile)
				s.wg.Go(func() {
					kubeproxyService.Run(kubeletReadyCh)
				})
			},
			readyCh: kubeproxyReadyCh,
		},
	}

	for _, svc := range services {
		log.Info().Str("component", "kubesolo").Msgf("starting %s...", svc.name)
		svc.start()
		if !waitForService(ctx, svc.name, svc.readyCh) {
			return
		}
	}

	log.Info().Str("component", "kubesolo").Msg("deploying coredns...")
	if err := coredns.Deploy(s.embedded.AdminKubeconfigFile); err != nil {
		log.Fatal().Err(err).Msg("failed to deploy coredns")
	}

	if s.localStorage {
		log.Info().Str("component", "kubesolo").Msg("deploying local path...")
		if err := localpath.Deploy(s.embedded.AdminKubeconfigFile, s.embedded.LocalPathStorageDir, s.localStorageSharedPath); err != nil {
			log.Fatal().Err(err).Msg("failed to deploy local path")
		}
	}

	if s.portainerEdgeID != "" && s.portainerEdgeKey != "" {
		log.Info().Str("component", "kubesolo").Msg("deploying portainer edge agent...")
		if err := portainer.DeployEdgeAgent(s.embedded.AdminKubeconfigFile, types.EdgeAgentConfig{
			EdgeID:           s.portainerEdgeID,
			EdgeKey:          s.portainerEdgeKey,
			EdgeAsync:        s.portainerEdgeAsync,
			EdgeInsecurePoll: "true",
		}); err != nil {
			log.Fatal().Err(err).Msg("failed to deploy portainer edge agent...")
		}
	}

	<-sigCh
	log.Info().Str("component", "kubesolo").Msg("shutting down...")

	// Wait for all service goroutines to complete gracefully
	log.Info().Str("component", "kubesolo").Msg("waiting for all services to shutdown...")
	s.wg.Wait()
	log.Info().Str("component", "kubesolo").Msg("all services have shutdown gracefully")
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

	// System Node IP
	nodeIP, err := network.GetNodeIP()
	if err != nil {
		log.Warn().Err(err).Msg("failed to get node IP address, using default loopback IP address")
	}

	// Disable OpenTelemetry SDK to prevent it from interfering with the application's logging
	os.Setenv("OTEL_SDK_DISABLED", "true")

	// Setup paths
	basePath := *flags.Path
	s.embedded = types.Embedded{
		// System Node IP
		NodeIP: nodeIP,

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
		ContainerdDir:            filepath.Join(basePath, types.DefaultContainerdDir),
		ContainerdSocketFile:     filepath.Join(basePath, types.DefaultContainerdDir, types.DefaultContainerdSocket),
		ContainerdBinaryFile:     filepath.Join(basePath, types.DefaultContainerdDir, "containerd"),
		ContainerdImagesDir:      filepath.Join(basePath, types.DefaultContainerdDir, "images"),
		ContainerdShimBinaryFile: filepath.Join(basePath, types.DefaultContainerdDir, "containerd-shim-runc-v2"),
		ContainerdConfigFile:     filepath.Join(basePath, types.DefaultContainerdDir, "config.toml"),
		ContainerdRootDir:        filepath.Join(basePath, types.DefaultContainerdDir, "root"),
		ContainerdStateDir:       filepath.Join(basePath, types.DefaultContainerdDir, "state"),

		// CNI paths
		ContainerdCNIDir:        filepath.Join(basePath, types.DefaultContainerdDir, "cni"),
		ContainerdCNIPluginsDir: filepath.Join(basePath, types.DefaultContainerdDir, "cni", "plugins"),
		ContainerdCNIConfigDir:  filepath.Join(basePath, types.DefaultContainerdDir, "cni", "conf"),
		ContainerdCNIConfigFile: filepath.Join(basePath, types.DefaultContainerdDir, "cni", "conf", types.DefaultCNIConfigName),

		// Runc binary
		RuncBinaryFile: filepath.Join(basePath, types.DefaultContainerdDir, "runc"),

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
		PortainerAgentImageFile:       filepath.Join(basePath, types.DefaultContainerdDir, "images", "portainer-agent.tar.gz"),
		CorednsImageFile:              filepath.Join(basePath, types.DefaultContainerdDir, "images", "coredns.tar.gz"),
		SandboxImageFile:              filepath.Join(basePath, types.DefaultContainerdDir, "images", "pause.tar.gz"),
		LocalPathProvisionerImageFile: filepath.Join(basePath, types.DefaultContainerdDir, "images", "local-path-provisioner.tar.gz"),

		// Load Balancer
		LoadBalancer: s.loadBalancer,

		// Local Path Storage
		LocalPathStorageDir: filepath.Join(basePath, types.DefaultLocalPathStorageDir),

		// Portainer Edge
		IsPortainerEdge: s.portainerEdgeID != "" && s.portainerEdgeKey != "",
	}
}
