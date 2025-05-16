package embedded

import "github.com/portainer/kubesolo/types"

// generateCNIConfigFile generates the default CNI configuration file
func generateCNIConfigFile() map[string]any {
	return map[string]any{
		"cniVersion": "1.0.0",
		"name":       "kubesolo-net",
		"plugins": []map[string]any{
			{
				"type":        "bridge",
				"bridge":      "cni0",
				"isGateway":   true,
				"ipMasq":      true,
				"hairpinMode": true,
				"capabilities": map[string]any{
					"portMappings": true,
					"ips":          true,
				},
				"ipam": map[string]any{
					"type": "host-local",
					"ranges": [][]map[string]any{
						{
							{
								"subnet": types.DefaultPodCIDR,
							},
						},
					},
					"routes": []map[string]any{
						{
							"dst": "0.0.0.0/0",
						},
					},
				},
			},
			{
				"type": "portmap",
				"capabilities": map[string]any{
					"portMappings": true,
				},
			},
			{
				"type": "loopback",
			},
		},
	}
}
