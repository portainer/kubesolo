package network

import (
	"errors"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
)

var ipv6SysctlPaths = []string{
	"/proc/sys/net/ipv6/conf/all/disable_ipv6",
	"/proc/sys/net/ipv6/conf/default/disable_ipv6",
	"/proc/sys/net/ipv6/conf/lo/disable_ipv6",
}

// DisableIPv6Sysctls writes 1 to the kernel sysctl paths that disable IPv6 on
// all interfaces. It is idempotent — paths already set to 1 are skipped. All
// paths are attempted regardless of failure; errors are aggregated and returned.
func DisableIPv6Sysctls() error {
	var errs []error
	for _, path := range ipv6SysctlPaths {
		current, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				log.Debug().Str("component", "network").Msgf("ipv6 sysctl not available, skipping: %s", path)
				continue
			}
		} else if len(current) > 0 && current[0] == '1' {
			log.Debug().Str("component", "network").Msgf("ipv6 already disabled: %s", path)
			continue
		}
		if err := os.WriteFile(path, []byte("1"), 0644); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		log.Info().Str("component", "network").Msgf("disabled ipv6: %s", path)
	}
	return errors.Join(errs...)
}
