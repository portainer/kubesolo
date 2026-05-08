package network

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
)

var ipv6SysctlPaths = []string{
	"/proc/sys/net/ipv6/conf/all/disable_ipv6",
	"/proc/sys/net/ipv6/conf/default/disable_ipv6",
	"/proc/sys/net/ipv6/conf/lo/disable_ipv6",
}

func DisableIPv6Sysctls() error {
	for _, path := range ipv6SysctlPaths {
		current, err := os.ReadFile(path)
		if err == nil && len(current) > 0 && current[0] == '1' {
			log.Debug().Str("component", "network").Msgf("ipv6 already disabled: %s", path)
			continue
		}
		if err := os.WriteFile(path, []byte("1"), 0644); err != nil {
			return fmt.Errorf("failed to disable ipv6 at %s: %w", path, err)
		}
		log.Info().Str("component", "network").Msgf("disabled ipv6: %s", path)
	}
	return nil
}
