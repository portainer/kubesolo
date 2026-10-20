package gpu

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/pkg/components/gpu/host"
	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Namespace holds the device plugin. It is KubeSolo's own, so turning GPU
// support off deletes it outright.
const Namespace = "kubesolo-gpu"

// plugin describes one vendor's device plugin.
type plugin struct {
	// name is the DaemonSet's name and the value of its `app` label.
	name string

	// resource is what the plugin advertises on the node and pods request.
	resource corev1.ResourceName
}

var plugins = map[string]plugin{
	types.GPUVendorNVIDIA: {
		name:     "nvidia-device-plugin",
		resource: "nvidia.com/gpu",
	},
	types.GPUVendorAMD: {
		name:     "amdgpu-device-plugin",
		resource: "amd.com/gpu",
	},
}

// ResourceName is the extended resource a pod requests to get one of the
// vendor's GPUs.
func ResourceName(vendor string) corev1.ResourceName {
	return plugins[vendor].resource
}

// CheckHost reports what the host is missing for the vendor's device plugin to
// work.
func CheckHost(vendor string) error {
	return host.Check(vendor)
}

// Deploy reconciles the vendor's device plugin and removes any other vendor's.
func Deploy(adminKubeconfig, vendor string) error {
	time.Sleep(types.DefaultComponentSleep)

	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	return reconcile(ctx, clientset, vendor)
}

// Remove deletes everything Deploy created. It runs on every start with GPU
// support off.
func Remove(adminKubeconfig string) error {
	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	return remove(ctx, clientset)
}

func reconcile(ctx context.Context, clientset kubernetes.Interface, vendor string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   Namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "kubesolo"},
		},
	}
	_, err := clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to reconcile gpu Namespace: %v", err)
	}

	for other, p := range plugins {
		if other == vendor {
			continue
		}
		err := clientset.AppsV1().DaemonSets(Namespace).Delete(ctx, p.name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to remove the %s device plugin: %v", other, err)
		}
	}

	if err := createDaemonSet(ctx, clientset, vendor); err != nil {
		return fmt.Errorf("failed to reconcile the %s device plugin: %v", vendor, err)
	}

	return nil
}

func remove(ctx context.Context, clientset kubernetes.Interface) error {
	err := clientset.CoreV1().Namespaces().Delete(ctx, Namespace, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete gpu Namespace: %v", err)
	}
	return nil
}
