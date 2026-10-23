package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/types"
)

func TestCheckHost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vendor  string
		files   []string
		missing string
	}{
		{"nvidia ready", types.GPUVendorNVIDIA, []string{"dev/nvidiactl", "usr/bin/nvidia-ctk"}, ""},
		{"nvidia without a driver", types.GPUVendorNVIDIA, []string{"usr/bin/nvidia-ctk"}, "/dev/nvidiactl"},
		{"nvidia without the toolkit", types.GPUVendorNVIDIA, []string{"dev/nvidiactl"}, "/usr/bin/nvidia-ctk"},
		{"amd ready", types.GPUVendorAMD, []string{"dev/kfd"}, ""},
		{"amd without a driver", types.GPUVendorAMD, nil, "/dev/kfd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range tc.files {
				p := filepath.Join(root, f)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := check(root, tc.vendor)
			if tc.missing == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.missing) {
				t.Errorf("error should name %s, got: %v", tc.missing, err)
			}
		})
	}
}
