package system

import (
	"os"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// GetHostname returns the hostname of the machine
// it returns the hostname of the machine
// if it fails, it uses the default value "kubesolo-node"
func GetHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Warn().Str("component", "kubesolo").Msg("failed to get hostname, using default value")
		hostname = types.DefaultNodeName
	}
	return hostname
}
