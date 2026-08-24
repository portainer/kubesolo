//go:build external_deps

package runtime

import (
	"context"

	"github.com/portainer/kubesolo/pkg/runtime/external"
	"github.com/portainer/kubesolo/types"
)

// NewService returns the container runtime kubesolo attaches to. Builds carrying the
// external_deps tag embed no runtime artifacts and deliberately do not import
// containerd, so there is no embedded runtime to fall back to.
func NewService(ctx context.Context, cancel context.CancelFunc, runtimeReady chan<- struct{}, embedded *types.Embedded) Service {
	return external.NewService(ctx, cancel, runtimeReady, embedded)
}
