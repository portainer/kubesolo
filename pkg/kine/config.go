package kine

import (
	"fmt"
	"time"

	"github.com/k3s-io/kine/pkg/app"
	"github.com/k3s-io/kine/pkg/drivers/generic"
	"github.com/k3s-io/kine/pkg/drivers/sqlite"
	"github.com/k3s-io/kine/pkg/endpoint"
)

// generateKineConfig generates the kine config for the kine service
// it starts from kine's own CLI defaults so that upstream defaults
// (poll batch size, compaction retention, emulated etcd version, ...) are picked up on version bumps
// connectionPoolConfig sets the connection pool config is customized to 3 idle connections and 5 open connections
// notifyInterval sets the notify interval to 15 seconds
// compaction is left to the apiserver: kine ignores its compact requests while kine's own compactor runs
// the DSN params come from kine's own defaults so that upstream tuning
// (statement cache size, synchronous mode, ...) is picked up on version bumps
func (s *service) generateKineConfig() endpoint.Config {
	config := app.Config(nil)
	config.Endpoint = fmt.Sprintf("sqlite://%s/state.db?%s", s.databaseDir, sqlite.DefaultParams)
	config.Listener = s.listener
	config.ConnectionPoolConfig = generic.ConnectionPoolConfig{
		MaxIdle:     3,
		MaxOpen:     5,
		MaxLifetime: 60 * time.Second,
	}
	config.NotifyInterval = 15 * time.Second
	config.CompactTimeout = 60 * time.Second
	return config
}
