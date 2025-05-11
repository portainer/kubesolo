package apiserver

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

// certificateData holds the contents of certificate files
type certificateData struct {
	adminCert []byte
	adminKey  []byte
	ca        []byte
}

// generateKubeConfig creates an admin kubeconfig file at the specified location
func (s *service) generateKubeConfig() error {
	if err := s.verifyCertificateFiles(); err != nil {
		return err
	}

	certData, err := s.readCertificateFiles()
	if err != nil {
		return err
	}

	kubeConfig := s.createKubeConfig(certData)
	if err := s.writeKubeConfig(kubeConfig); err != nil {
		return err
	}

	log.Info().Str("component", "apiserver").Msgf("kubeconfig file created at %s", s.adminKubeconfig)
	return nil
}

// verifyCertificateFiles checks if all required certificate files exist
func (s *service) verifyCertificateFiles() error {
	for _, path := range []string{s.adminCertFile, s.adminKeyFile, s.caFile} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("required certificate file not found: %s", path)
		}
	}
	return nil
}

// readCertificateFiles reads all required certificate files
func (s *service) readCertificateFiles() (*certificateData, error) {
	adminCert, err := os.ReadFile(s.adminCertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read admin certificate: %v", err)
	}

	adminKey, err := os.ReadFile(s.adminKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read admin key: %v", err)
	}

	ca, err := os.ReadFile(s.caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %v", err)
	}

	return &certificateData{
		adminCert: adminCert,
		adminKey:  adminKey,
		ca:        ca,
	}, nil
}

// createKubeConfig creates a new kubeconfig with the provided certificate data
func (s *service) createKubeConfig(certData *certificateData) *api.Config {
	const (
		clusterName  = "kubesolo"
		userName     = "kubernetes-admin"
		contextName  = "kubernetes-admin@" + clusterName
		tokenUser    = "admin-token"
		tokenContext = "admin-token@" + clusterName
	)

	kubeConfig := api.NewConfig()
	kubeConfig.Clusters[clusterName] = &api.Cluster{
		Server:                   types.DefaultAPIServerAddress,
		CertificateAuthorityData: certData.ca,
	}
	kubeConfig.AuthInfos[userName] = &api.AuthInfo{
		ClientCertificateData: certData.adminCert,
		ClientKeyData:         certData.adminKey,
	}
	kubeConfig.Contexts[contextName] = &api.Context{
		Cluster:  clusterName,
		AuthInfo: userName,
	}
	kubeConfig.AuthInfos[tokenUser] = &api.AuthInfo{
		Token: "admin-token",
	}
	kubeConfig.Contexts[tokenContext] = &api.Context{
		Cluster:  clusterName,
		AuthInfo: tokenUser,
	}
	kubeConfig.CurrentContext = contextName

	return kubeConfig
}

// writeKubeConfig writes the kubeconfig to the specified file
func (s *service) writeKubeConfig(config *api.Config) error {
	if err := clientcmd.WriteToFile(*config, s.adminKubeconfig); err != nil {
		return fmt.Errorf("failed to write kubeconfig file: %v", err)
	}
	return nil
}
