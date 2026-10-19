package embedded

// generateCNIConfigFile generates the default CNI configuration file, its pods'
// addresses from podCIDR
func generateCNIConfigFile(mtu int, podCIDR string) map[string]any {
	return map[string]any{
		"cniVersion": "1.0.0",
		"name":       "kubesolo-net",
		"plugins": []map[string]any{
			{
				"type":        "bridge",
				"bridge":      "cni0",
				"isGateway":   true,
				"ipMasq":      false,
				"hairpinMode": true,
				"mtu":         mtu,
				"capabilities": map[string]any{
					"portMappings": true,
					"ips":          true,
				},
				"ipam": map[string]any{
					"type": "host-local",
					"ranges": [][]map[string]any{
						{
							{
								"subnet": podCIDR,
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
