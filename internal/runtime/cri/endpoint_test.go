package cri

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveValid(t *testing.T) {
	tests := []struct {
		name       string
		flag       string
		url        string
		socketPath string
		external   bool
	}{
		{
			name: "empty flag means embedded containerd",
		},
		{
			name:       "unix URL",
			flag:       "unix:///run/crio/crio.sock",
			url:        "unix:///run/crio/crio.sock",
			socketPath: "/run/crio/crio.sock",
			external:   true,
		},
		{
			name:       "bare absolute path gains the unix scheme",
			flag:       "/run/containerd/containerd.sock",
			url:        "unix:///run/containerd/containerd.sock",
			socketPath: "/run/containerd/containerd.sock",
			external:   true,
		},
		{
			name:       "surrounding whitespace is ignored",
			flag:       "  unix:///run/crio/crio.sock\n",
			url:        "unix:///run/crio/crio.sock",
			socketPath: "/run/crio/crio.sock",
			external:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint, err := Resolve(test.flag)
			require.NoError(t, err)
			assert.Equal(t, test.url, endpoint.URL)
			assert.Equal(t, test.socketPath, endpoint.SocketPath)
			assert.Equal(t, test.external, endpoint.External)
		})
	}
}

func TestResolveInvalid(t *testing.T) {
	for _, flag := range []string{
		"tcp://127.0.0.1:1234",
		"npipe:////./pipe/containerd-containerd",
		"unix://run/crio/crio.sock", // two slashes, so the path is relative
		"run/crio/crio.sock",
	} {
		t.Run(flag, func(t *testing.T) {
			_, err := Resolve(flag)
			assert.Error(t, err, "endpoint %q must be rejected", flag)
		})
	}
}

func TestEmbedded(t *testing.T) {
	endpoint := Embedded("/var/lib/kubesolo/containerd/containerd.sock")

	assert.Equal(t, "unix:///var/lib/kubesolo/containerd/containerd.sock", endpoint.URL)
	assert.Equal(t, "/var/lib/kubesolo/containerd/containerd.sock", endpoint.SocketPath)
	assert.False(t, endpoint.External, "the embedded containerd is not a host-managed runtime")
}
