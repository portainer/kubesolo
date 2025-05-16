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
			MaxIdle:     2,
			MaxOpen:     3,
			MaxLifetime: 10 * time.Second,
		},
		NotifyInterval: 10 * time.Second,
	}
}
