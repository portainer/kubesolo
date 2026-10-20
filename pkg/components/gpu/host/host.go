// Package host checks that a host can give its GPU to pods. It is apart from
// package gpu, which deploys the device plugins, so that kubesoloctl can check
// a host before installing without linking a Kubernetes client.
package host

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/portainer/kubesolo/types"
)

// requires are, for each vendor, the host files its device plugin cannot work
// without, and why.
var requires = map[string]map[string]string{
	types.GPUVendorNVIDIA: {
		"/dev/nvidiactl": "the NVIDIA driver is not loaded",
		// The CDI spec the plugin writes runs this on the host as a hook.
		"/usr/bin/nvidia-ctk": "the NVIDIA Container Toolkit is not installed",
	},
	types.GPUVendorAMD: {
		"/dev/kfd": "the amdgpu driver is not loaded, or the GPU does not support ROCm",
	},
}

// Check reports what the host is missing for the vendor's device plugin to
// work. Deploying it anyway would leave the GPU unadvertised and every pod
// that requests one Pending, with nothing to say why.
func Check(vendor string) error {
	return check("/", vendor)
}

func check(root, vendor string) error {
	for _, path := range slices.Sorted(maps.Keys(requires[vendor])) {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			return fmt.Errorf("%s not found: %s", path, requires[vendor][path])
		}
	}
	return nil
}
