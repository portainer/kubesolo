package apiserver

import (
	"context"

	"github.com/portainer/kubesolo/types"
)

type service struct {
	apiServerReady        chan struct{}
	ctx                   context.Context
	cancel                context.CancelFunc
	serverPath            string
	nodeName              string
	pkiAPIServerDir       string
	caFile                string
	apiServerCertFile     string
	apiServerKeyFile      string
	adminCertFile         string
	adminKeyFile          string
	adminKubeconfig       string
	serviceAccountKeyFile string
	kubeSoloWebhook       *webhoook
}

func NewService(ctx context.Context, cancel context.CancelFunc, apiServerReady chan struct{}, nodeName string, embedded types.Embedded) *service {
	return &service{
		apiServerReady:        apiServerReady,
		ctx:                   ctx,
		cancel:                cancel,
		serverPath:            embedded.APIServerDir,
		nodeName:              nodeName,
		pkiAPIServerDir:       embedded.PKIAPIServerDir,
		caFile:                embedded.CACerts.Cert,
		apiServerCertFile:     embedded.APIServerCerts.Cert,
		apiServerKeyFile:      embedded.APIServerCerts.Key,
		adminCertFile:         embedded.AdminCerts.Cert,
		adminKeyFile:          embedded.AdminCerts.Key,
		adminKubeconfig:       embedded.AdminKubeconfigFile,
		serviceAccountKeyFile: embedded.ServiceAccountKeyFile,
		kubeSoloWebhook:       newWebhook(nodeName, embedded.PKIDir),
	}
}
