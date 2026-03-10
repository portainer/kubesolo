package kine

import (
	"fmt"
	"time"

	"github.com/k3s-io/kine/pkg/drivers/generic"
	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/portainer/kubesolo/types"
)

const (
	notifyInterval   = 15 * time.Second
	compactInterval  = 5 * time.Minute
	compactBatchSize = int64(1000)
	compactMinRetain = int64(1000)
)

// generateKineConfig generates the kine endpoint config for the SQLite backend.
func (s *service) generateKineConfig() endpoint.Config {
	return endpoint.Config{
		Endpoint: fmt.Sprintf("sqlite://%s/state.db?_journal=WAL&cache=shared&_busy_timeout=30000&_txlock=immediate", s.databaseDir),
		Listener: types.DefaultKineEndpoint,
		ConnectionPoolConfig: generic.ConnectionPoolConfig{
			MaxIdle:     3,
			MaxOpen:     5,
			MaxLifetime: 60 * time.Second,
		},
		NotifyInterval:   notifyInterval,
		CompactBatchSize: compactBatchSize,
	}
}
