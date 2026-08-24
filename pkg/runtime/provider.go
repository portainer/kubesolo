//go:build !external_deps

package runtime

import (
	"context"

	"github.com/portainer/kubesolo/pkg/runtime/containerd"
	"github.com/portainer/kubesolo/pkg/runtime/external"
	"github.com/portainer/kubesolo/types"
)

// NewService returns the container runtime kubesolo depends on: a runtime the host
// manages when --container-runtime-endpoint is set, otherwise the containerd kubesolo
// embeds and supervises itself.
func NewService(ctx context.Context, cancel context.CancelFunc, runtimeReady chan<- struct{}, embedded *types.Embedded) Service {
	if embedded.RuntimeExternal {
		return external.NewService(ctx, cancel, runtimeReady, embedded)
	}

	return containerd.NewService(ctx, cancel, runtimeReady, embedded)
}
