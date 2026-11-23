package kine

import (
	"fmt"
	"time"

	"github.com/k3s-io/kine/pkg/app"
	"github.com/k3s-io/kine/pkg/drivers/sqlite"
	"github.com/k3s-io/kine/pkg/endpoint"
)

// generateKineConfig generates the kine config for the kine service
// it starts from kine's own CLI defaults so that upstream defaults
// (poll batch size, connection pool, notify interval, ...) are picked up on version bumps
// compaction is left to the apiserver: kine ignores its compact requests while kine's own compactor runs,
// which kine's default CompactInterval of 0 keeps off
// compactTimeout is raised to 60 seconds
// the DSN params come from kine's own defaults so that upstream tuning
// (statement cache size, synchronous mode, ...) is picked up on version bumps
func (s *service) generateKineConfig() endpoint.Config {
	config := app.Config(nil)
	config.Endpoint = fmt.Sprintf("sqlite://%s/state.db?%s", s.databaseDir, sqlite.DefaultParams)
	config.Listener = s.listener
	config.CompactTimeout = 60 * time.Second
	return config
}
