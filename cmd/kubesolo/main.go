package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	rdebug "runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/portainer/kubesolo/internal/config/flags"
	"github.com/portainer/kubesolo/internal/core/embedded"
	"github.com/portainer/kubesolo/internal/core/pki"
	"github.com/portainer/kubesolo/internal/logging"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/pkg/components/coredns"
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

type kubesolo struct {
	hostName         string
	debug            bool
	pprofServer      bool
	portainerEdgeID  string
	portainerEdgeKey string
	embedded         types.Embedded

	// Memory management settings
	gcPercent        int
	memoryLimit      int64
	idleMemoryCheck  bool
}

var (
	containerdReadyCh = make(chan struct{})
	kineReadyCh       = make(chan struct{})
	apiServerReadyCh  = make(chan struct{})
	kubeletReadyCh    = make(chan struct{})
	controllerReadyCh = make(chan struct{})
	kubeproxyReadyCh  = make(chan struct{})
)

func service() (*kubesolo, error) {
	return &kubesolo{
		hostName:         system.GetHostname(),
		debug:            *flags.Debug,
		pprofServer:      *flags.PprofServer,
		portainerEdgeID:  *flags.PortainerEdgeID,
		portainerEdgeKey: *flags.PortainerEdgeKey,

		// Default memory settings - will be overridden by env vars if present
		gcPercent:        types.DefaultGCPercent,
		memoryLimit:      types.DefaultMemoryLimit,
		idleMemoryCheck:  true,
	}, nil
}

func main() {
	kingpin.MustParse(flags.Application.Parse(os.Args[1:]))

	service, err := service()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create service. check the logs for more information. exiting...")
	}

	service.bootstrap()
	service.run()
}

// configureMemorySettings applies memory management settings from environment variables
// and configures the Go runtime accordingly
func (s *kubesolo) configureMemorySettings() {
	// Check for GOGC environment variable (percentage for garbage collection)
	if gogcStr := os.Getenv("GOGC"); gogcStr != "" {
		if gogc, err := strconv.Atoi(gogcStr); err == nil && gogc >= 0 {
			s.gcPercent = gogc
			log.Info().Int("value", s.gcPercent).Msg("using GOGC environment variable")
		}
	}

	// Check for GOMEMLIMIT environment variable (memory limit in bytes)
	if memlimitStr := os.Getenv("GOMEMLIMIT"); memlimitStr != "" {
		if memlimit, err := strconv.ParseInt(memlimitStr, 10, 64); err == nil && memlimit > 0 {
			s.memoryLimit = memlimit
			log.Info().Int64("value", s.memoryLimit).Msg("using GOMEMLIMIT environment variable")
		}
	}

	// Check for KUBESOLO_IDLE_MEMORY_CHECK environment variable
	if idleCheckStr := os.Getenv("KUBESOLO_IDLE_MEMORY_CHECK"); idleCheckStr != "" {
		s.idleMemoryCheck = idleCheckStr == "1" || idleCheckStr == "true" || idleCheckStr == "yes"
	}

	// Apply settings to runtime
	rdebug.SetGCPercent(s.gcPercent)
	rdebug.SetMemoryLimit(s.memoryLimit)

	// Start idle memory check if enabled
	if s.idleMemoryCheck {
		go s.startIdleMemoryCheck()
	}
}

// startIdleMemoryCheck periodically frees memory when the system is idle
func (s *kubesolo) startIdleMemoryCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C
		// In a real implementation, we would check some metrics to determine if the system is idle
		// For simplicity, we'll just periodically free memory
		log.Debug().Msg("performing idle memory cleanup")
		rdebug.FreeOSMemory()
	}
}

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
				go containerdService.Run()
			},
			readyCh: containerdReadyCh,
		},
		{
			name: "kine",
			start: func() {
				kineService := kine.NewService(ctx, cancel, s.embedded.KineDir, kineReadyCh)
				go kineService.Run()
			},
			readyCh: kineReadyCh,
		},
		{
			name: "apiserver",
			start: func() {
				apiserverService := apiserver.NewService(ctx, cancel, apiServerReadyCh, s.hostName, s.embedded)
				go apiserverService.Run(kineReadyCh)
			},
			readyCh: apiServerReadyCh,
		},
		{
			name: "controller",
			start: func() {
				controllerService := controller.NewService(ctx, cancel, controllerReadyCh, s.embedded.ControllerDir, s.embedded)
				go controllerService.Run(apiServerReadyCh)
			},
			readyCh: controllerReadyCh,
		},
		{
			name: "kubelet",
			start: func() {
				kubeletService := kubelet.NewService(ctx, cancel, kubeletReadyCh, &s.embedded)
				go kubeletService.Run(apiServerReadyCh)
			},
			readyCh: kubeletReadyCh,
		},
		{
			name: "kubeproxy",
			start: func() {
				kubeproxyService := kubeproxy.NewService(ctx, cancel, kubeproxyReadyCh, s.embedded.AdminKubeconfigFile)
				go kubeproxyService.Run(kubeletReadyCh)
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

	if s.portainerEdgeID != "" && s.portainerEdgeKey != "" {
		log.Info().Str("component", "kubesolo").Msg("deploying portainer edge agent...")
		if err := portainer.DeployEdgeAgent(s.embedded.AdminKubeconfigFile, types.EdgeAgentConfig{
			EdgeID:           s.portainerEdgeID,
			EdgeKey:          s.portainerEdgeKey,
			EdgeInsecurePoll: "true",
		}); err != nil {
			log.Fatal().Err(err).Msg("failed to deploy portainer edge agent...")
		}
	}

	<-sigCh
	log.Info().Str("component", "kubesolo").Msg("shutting down...")
}

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

func (s *kubesolo) bootstrap() {
	if s.debug {
		log.Info().Msg("debug mode enabled")
		logging.SetLoggingLevel("DEBUG")
	}

	if s.pprofServer {
		system.StartMonitoring()
	}

	// Configure runtime
	s.configureMemorySettings()
	rdebug.FreeOSMemory()

	// Setup logging
	logging.ConfigureLogger()
	logging.SetLoggingMode("PRETTY")
	logging.SetLoggingLevel("INFO")
	logging.ConfigureK8sDefaultLogging()

	// Setup paths
	basePath := *flags.Path
	s.embedded = types.Embedded{
		// System paths
		SystemCNIDir: types.DefaultSystemCNIDir,

		// Admin kubeconfig file
		AdminKubeconfigFile: filepath.Join(basePath, types.DefaultPKIDir, "admin", "admin.kubeconfig"),

		// PKI paths
		PKIDir:           filepath.Join(basePath, types.DefaultPKIDir),
		PKICADir:         filepath.Join(basePath, types.DefaultPKIDir, "ca"),
		PKIAdminDir:      filepath.Join(basePath, types.DefaultPKIDir, "admin"),
		PKIAPIServerDir:  filepath.Join(basePath, types.DefaultPKIDir, "apiserver"),
		PKIControllerDir: filepath.Join(basePath, types.DefaultPKIDir, "controller-manager"),
		PKIKubeletDir:    filepath.Join(basePath, types.DefaultPKIDir, "kubelet"),
		PKIWebhookDir:    filepath.Join(basePath, types.DefaultPKIDir, "webhook"),

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

		// Kine paths
		KineDir:        filepath.Join(basePath, types.KubesoloKineDir),
		KineSocketFile: filepath.Join(basePath, types.KubesoloKineDir, "socket"),

		// Controller manager paths
		ControllerDir: filepath.Join(basePath, types.KubesoloControllerManagerDir),

		// Webhook paths
		WebhookDir: filepath.Join(basePath, types.KubesoloWebhookDir),

		// Image paths
		PortainerAgentImageFile: filepath.Join(basePath, types.DefaultContainerdDir, "images", "portainer-agent.tar.gz"),
		CorednsImageFile:        filepath.Join(basePath, types.DefaultContainerdDir, "images", "coredns.tar.gz"),

		// Portainer Edge
		IsPortainerEdge: s.portainerEdgeID != "" && s.portainerEdgeKey != "",
	}
}
