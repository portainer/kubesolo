package kubelet

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kubeletArgs returns the arguments configureKubeletArgs sets on a command.
func kubeletArgs(t *testing.T, s *service) []string {
	t.Helper()

	var got []string
	command := &cobra.Command{
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			got = args
			return nil
		},
	}
	s.configureKubeletArgs(command)
	require.NoError(t, command.Execute())

	return got
}

func TestConfigureKubeletArgs_ConfigDir(t *testing.T) {
	s := &service{
		kubeletConfigFile: "/var/lib/kubesolo/kubelet/config/config.yaml",
		kubeletDropInDir:  "/var/lib/kubesolo/kubelet/config.d",
	}

	args := kubeletArgs(t, s)

	i := slices.Index(args, "--config-dir")
	require.NotEqual(t, -1, i, "the kubelet must be given the drop-in directory")
	require.Less(t, i+1, len(args))
	assert.Equal(t, "/var/lib/kubesolo/kubelet/config.d", args[i+1])
}
