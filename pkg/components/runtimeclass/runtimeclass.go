package runtimeclass

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Deploy creates the wasmtime RuntimeClass resource in the cluster
func Deploy(ctx context.Context, adminKubeconfig string) error {
	time.Sleep(types.DefaultComponentSleep)

	ctx, cancel := context.WithTimeout(ctx, types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	log.Info().Str("component", "runtimeclass").Msg("deploying RuntimeClass wasmtime")

	rc := &nodeapi.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "wasmtime",
		},
		Handler: "wasmtime",
	}

	_, err = clientset.NodeV1().RuntimeClasses().Create(ctx, rc, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to create RuntimeClass wasmtime: %v", err)
	}

	return nil
}
