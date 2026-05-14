package kine

import (
	"fmt"
	"time"

	"github.com/k3s-io/kine/pkg/drivers/generic"
	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/portainer/kubesolo/types"
)

// generateKineConfig generates the kine config for the kine service
// connectionPoolConfig sets the connection pool config is customized to 2 idle connections and 3 open connections
// notifyInterval sets the notify interval to 10 seconds
func (s *service) generateKineConfig() endpoint.Config {
	return endpoint.Config{
		Endpoint: fmt.Sprintf("sqlite://%s/state.db?_journal=WAL&cache=shared&_busy_timeout=30000&_txlock=immediate", s.databaseDir),
		Listener: types.DefaultKineEndpoint,
		ConnectionPoolConfig: generic.ConnectionPoolConfig{
			MaxIdle:     3,
			MaxOpen:     5,
			MaxLifetime: 60 * time.Second,
		},
		NotifyInterval:   15 * time.Second,
		CompactInterval:  5 * time.Minute,
		CompactTimeout:   60 * time.Second,
		CompactBatchSize: 1000,
	}
}
