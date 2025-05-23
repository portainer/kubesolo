package controller

import (
	"context"

	"github.com/portainer/kubesolo/types"
)

// service is the service for the controller manager
type service struct {
	ctx                       context.Context
	cancel                    context.CancelFunc
	controllerReady           chan<- struct{}
	controllerDir             string
	controllerManagerCertFile string
	controllerManagerKeyFile  string
	caFile                    string
	adminKubeconfigFile       string
	serviceAccountKeyFile     string
}

// NewService creates a new controller service
func NewService(ctx context.Context, cancel context.CancelFunc, controllerReady chan<- struct{}, controllerDir string, embedded types.Embedded) *service {
	return &service{
		ctx:                       ctx,
		cancel:                    cancel,
		controllerReady:           controllerReady,
		controllerDir:             controllerDir,
		controllerManagerCertFile: embedded.ControllerManagerCerts.Cert,
		controllerManagerKeyFile:  embedded.ControllerManagerCerts.Key,
		caFile:                    embedded.CACerts.Cert,
		adminKubeconfigFile:       embedded.AdminKubeconfigFile,
		serviceAccountKeyFile:     embedded.ServiceAccountKeyFile,
	}
}
