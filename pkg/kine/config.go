package kine

import (
	"fmt"
	"time"

	"github.com/k3s-io/kine/pkg/drivers/generic"
	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/portainer/kubesolo/types"
)

func (s *service) generateKineConfig() endpoint.Config {
	return endpoint.Config{
		Endpoint: fmt.Sprintf("sqlite://%s/state.db?_journal=WAL&cache=shared&_busy_timeout=30000&_txlock=immediate", s.databaseDir),
		Listener: types.DefaultKineEndpoint,
		ConnectionPoolConfig: generic.ConnectionPoolConfig{
			MaxIdle:     5,
			MaxOpen:     5,
			MaxLifetime: 10 * time.Second,
		},
		NotifyInterval: 10 * time.Second,
	}
}
