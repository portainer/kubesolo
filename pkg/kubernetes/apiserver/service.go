package apiserver

import (
	"context"

	"github.com/portainer/kubesolo/pkg/kubernetes/webhook"
	"github.com/portainer/kubesolo/types"
)

// service is the service for the API server
type service struct {
	apiServerReady          chan struct{}
	ctx                     context.Context
	cancel                  context.CancelFunc
	serverPath              string
	nodeName                string
	pkiAPIServerDir         string
	caFile                  string
	apiServerCertFile       string
	apiServerKeyFile        string
	adminCertFile           string
	adminKeyFile            string
	adminKubeconfig         string
	serviceAccountKeyFile   string
	requestHeaderCAFile     string
	requestHeaderClientCert string
	requestHeaderClientKey  string
	kubeSoloWebhook         *webhook.Service
}

// NewService creates a new API server service
func NewService(ctx context.Context, cancel context.CancelFunc, apiServerReady chan struct{}, nodeName string, embedded types.Embedded) *service {
	return &service{
		apiServerReady:          apiServerReady,
		ctx:                     ctx,
		cancel:                  cancel,
		serverPath:              embedded.APIServerDir,
		nodeName:                nodeName,
		pkiAPIServerDir:         embedded.PKIAPIServerDir,
		caFile:                  embedded.CACerts.Cert,
		apiServerCertFile:       embedded.APIServerCerts.Cert,
		apiServerKeyFile:        embedded.APIServerCerts.Key,
		adminCertFile:           embedded.AdminCerts.Cert,
		adminKeyFile:            embedded.AdminCerts.Key,
		adminKubeconfig:         embedded.AdminKubeconfigFile,
		serviceAccountKeyFile:   embedded.ServiceAccountKeyFile,
		requestHeaderCAFile:     embedded.RequestHeaderCerts.CACert,
		requestHeaderClientCert: embedded.RequestHeaderCerts.ClientCert,
		requestHeaderClientKey:  embedded.RequestHeaderCerts.ClientKey,
		kubeSoloWebhook:         webhook.NewService(nodeName, embedded.PKIDir),
	}
}
