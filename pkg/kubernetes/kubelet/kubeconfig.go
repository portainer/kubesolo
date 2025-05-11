package kubelet

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

// generateKubeletKubeconfig creates a kubeconfig structure for the kubelet
func (s *service) generateKubeletKubeconfig() error {
	kubeconfigMap := map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []map[string]any{
			{
				"name": "kubernetes",
				"cluster": map[string]any{
					"certificate-authority": s.caFile,
					"server":                types.DefaultAPIServerAddress,
				},
			},
		},
		"users": []map[string]any{
			{
				"name": fmt.Sprintf("system:node:%s", s.nodeName),
				"user": map[string]any{
					"client-certificate": s.certFile,
					"client-key":         s.keyFile,
				},
			},
		},
		"contexts": []map[string]any{
			{
				"name": fmt.Sprintf("system:node:%s@kubernetes", s.nodeName),
				"context": map[string]any{
					"cluster": "kubernetes",
					"user":    fmt.Sprintf("system:node:%s", s.nodeName),
				},
			},
		},
		"current-context": fmt.Sprintf("system:node:%s@kubernetes", s.nodeName),
	}

	yamlData, err := yaml.Marshal(kubeconfigMap)
	if err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to marshal kubelet kubeconfig: %v", err)
		return fmt.Errorf("failed to marshal kubelet kubeconfig: %v", err)
	}

	if err := os.WriteFile(s.kubeletKubeConfigFile, yamlData, 0600); err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to write kubelet kubeconfig: %v", err)
		return fmt.Errorf("failed to write kubelet kubeconfig: %v", err)
	}

	log.Debug().Str("component", "kubelet").Msg("generated kubelet kubeconfig successfully")
	return nil
}
