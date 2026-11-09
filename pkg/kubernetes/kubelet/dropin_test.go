package kubelet

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dropInHeader = "apiVersion: kubelet.config.k8s.io/v1beta1\nkind: KubeletConfiguration\n"

// captureLogs redirects zerolog for the duration of a test and returns what was
// written. The logger is global, so this cannot run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	previous, previousLevel := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() {
		log.Logger = previous
		zerolog.SetGlobalLevel(previousLevel)
	})

	return buf
}

func TestCheckDropIns(t *testing.T) {
	tests := []struct {
		name          string
		files         map[string]string
		containerMode bool
		wantErr       []string
		wantLog       []string
		wantNoWarning bool
	}{
		{
			name:          "empty directory",
			wantNoWarning: true,
		},
		{
			name: "eviction and image GC are applied",
			files: map[string]string{
				"10-eviction.conf": dropInHeader + "evictionHard:\n  imagefs.available: 10%\nimageGCHighThresholdPercent: 80\n",
			},
			wantLog:       []string{"applying kubelet drop-in", "10-eviction.conf"},
			wantNoWarning: true,
		},
		{
			name: "drop-ins in subdirectories are checked",
			files: map[string]string{
				"nested/10-cpu.conf": dropInHeader + "cpuManagerPolicy: static\n",
			},
			wantErr: []string{"nested/10-cpu.conf", "cpuManagerPolicy"},
		},
		{
			name: "cpu manager policy is refused",
			files: map[string]string{
				"10-cpu.conf": dropInHeader + "cpuManagerPolicy: static\n",
			},
			wantErr: []string{"10-cpu.conf", "cpuManagerPolicy", "kubernetes.kubelet.cpuManager"},
		},
		{
			name: "cpu manager policy options are refused",
			files: map[string]string{
				"10-cpu.conf": dropInHeader + "cpuManagerPolicyOptions:\n  full-pcpus-only: \"true\"\n",
			},
			wantErr: []string{"cpuManagerPolicyOptions", "kubernetes.kubelet.cpuManager"},
		},
		{
			name: "reserved system CPUs are refused",
			files: map[string]string{
				"10-cpu.conf": dropInHeader + "reservedSystemCPUs: \"0\"\n",
			},
			wantErr: []string{"reservedSystemCPUs", "kubernetes.kubelet.cpuManager"},
		},
		{
			name: "missing apiVersion and kind are refused",
			files: map[string]string{
				"10-eviction.conf": "evictionHard:\n  imagefs.available: 10%\n",
			},
			wantErr: []string{"10-eviction.conf", "apiVersion: kubelet.config.k8s.io/v1beta1", "kind: KubeletConfiguration"},
		},
		{
			name: "a different kind is refused",
			files: map[string]string{
				"10-eviction.conf": "apiVersion: kubelet.config.k8s.io/v1beta1\nkind: CredentialProviderConfig\n",
			},
			wantErr: []string{"10-eviction.conf", "kind: KubeletConfiguration"},
		},
		{
			name: "empty file is refused",
			files: map[string]string{
				"10-empty.conf": "",
			},
			wantErr: []string{"10-empty.conf", "apiVersion"},
		},
		{
			name: "invalid YAML is refused",
			files: map[string]string{
				"10-broken.conf": dropInHeader + "evictionHard: [unclosed\n",
			},
			wantErr: []string{"10-broken.conf"},
		},
		{
			name: "overriding a setting KubeSolo controls warns",
			files: map[string]string{
				"10-runtime.conf": dropInHeader + "containerRuntimeEndpoint: unix:///run/other.sock\nclusterDNS: [\"10.0.0.10\"]\n",
			},
			wantLog: []string{"10-runtime.conf", "containerRuntimeEndpoint", "clusterDNS", `"level":"warn"`},
		},
		{
			name: "QoS cgroups warn in container mode",
			files: map[string]string{
				"10-qos.conf": dropInHeader + "cgroupsPerQOS: true\n",
			},
			containerMode: true,
			wantLog:       []string{"10-qos.conf", "cgroupsPerQOS", `"level":"warn"`},
		},
		{
			name: "QoS cgroups don't warn outside container mode",
			files: map[string]string{
				"10-qos.conf": dropInHeader + "cgroupsPerQOS: true\n",
			},
			wantNoWarning: true,
		},
		{
			name: "files the kubelet ignores warn",
			files: map[string]string{
				"10-eviction.yaml": dropInHeader + "imageGCHighThresholdPercent: 80\n",
			},
			wantLog: []string{"10-eviction.yaml", ".conf", `"level":"warn"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				path := filepath.Join(dir, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
			}
			logs := captureLogs(t)

			s := &service{kubeletDropInDir: dir, containerMode: tt.containerMode}
			err := s.checkDropIns()

			if len(tt.wantErr) > 0 {
				require.Error(t, err)
				for _, want := range tt.wantErr {
					assert.Contains(t, err.Error(), want)
				}
			} else {
				require.NoError(t, err)
			}
			for _, want := range tt.wantLog {
				assert.Contains(t, logs.String(), want)
			}
			if tt.wantNoWarning {
				assert.NotContains(t, logs.String(), `"level":"warn"`)
			}
		})
	}
}
