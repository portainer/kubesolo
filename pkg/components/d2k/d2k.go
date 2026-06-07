package d2k

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
)

const (
	// AppLabel is the value of the `app` label applied to every d2k resource.
	AppLabel = "d2k"

	// DeploymentName is the name of the d2k Deployment, ServiceAccount, Role,
	// RoleBinding, Service, and TLS Secret in the target namespace.
	DeploymentName = "d2k"

	// ServiceAccountName is the name of the d2k ServiceAccount.
	ServiceAccountName = "d2k"

	// RoleName is the name of the namespace-scoped Role granting d2k workload
	// management permissions.
	RoleName = "d2k"

	// RoleBindingName is the name of the namespace-scoped RoleBinding tying
	// the Role to the ServiceAccount.
	RoleBindingName = "d2k"

	// NodeReaderClusterRoleName is the cluster-scoped ClusterRole granting
	// read-only access to nodes and storageclasses.
	NodeReaderClusterRoleName = "d2k-node-reader"

	// NodeReaderClusterRoleBindingName binds NodeReaderClusterRoleName to the
	// d2k ServiceAccount.
	NodeReaderClusterRoleBindingName = "d2k-node-reader"

	// TLSSecretName is the name of the kubernetes.io/tls Secret holding the
	// d2k server certificate and key. The d2k Deployment mounts this Secret
	// at /etc/d2k/tls and switches to TLS on port 2376 when present.
	TLSSecretName = "d2k-tls"

	// ServiceName is the name of the LoadBalancer Service exposing the d2k
	// Docker-compatible API endpoint on port 2376.
	ServiceName = "d2k"
)

// Config carries the inputs that vary per kubesolo invocation.
type Config struct {
	// Namespace is the target namespace into which all d2k resources are
	// reconciled and against which d2k translates Docker API calls.
	Namespace string

	// Image is the fully-qualified d2k container image reference.
	Image string

	// Certs holds the on-disk paths to the kubesolo-CA-signed TLS material.
	Certs types.D2KCertificatePaths
}

// Deploy reconciles all d2k Kubernetes objects programmatically. The function
// is idempotent: repeated invocations against an existing deployment update
// in place rather than failing.
func Deploy(adminKubeconfig string, cfg Config) error {
	time.Sleep(types.DefaultComponentSleep)

	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := createNamespace(ctx, clientset, cfg.Namespace); err != nil {
		return fmt.Errorf("failed to reconcile d2k Namespace: %v", err)
	}

	if err := createServiceAccount(ctx, clientset, cfg.Namespace); err != nil {
		return fmt.Errorf("failed to reconcile d2k ServiceAccount: %v", err)
	}

	if err := createRole(ctx, clientset, cfg.Namespace); err != nil {
		return fmt.Errorf("failed to reconcile d2k Role: %v", err)
	}

	if err := createRoleBinding(ctx, clientset, cfg.Namespace); err != nil {
		return fmt.Errorf("failed to reconcile d2k RoleBinding: %v", err)
	}

	if err := createNodeReaderClusterRole(ctx, clientset); err != nil {
		return fmt.Errorf("failed to reconcile d2k ClusterRole: %v", err)
	}

	if err := createNodeReaderClusterRoleBinding(ctx, clientset, cfg.Namespace); err != nil {
		return fmt.Errorf("failed to reconcile d2k ClusterRoleBinding: %v", err)
	}

	if err := createTLSSecret(ctx, clientset, cfg.Namespace, cfg.Certs); err != nil {
		return fmt.Errorf("failed to reconcile d2k TLS Secret: %v", err)
	}

	if err := createDeployment(ctx, clientset, cfg.Namespace, cfg.Image); err != nil {
		return fmt.Errorf("failed to reconcile d2k Deployment: %v", err)
	}

	if err := createService(ctx, clientset, cfg.Namespace); err != nil {
		return fmt.Errorf("failed to reconcile d2k Service: %v", err)
	}

	return nil
}

// commonLabels returns the labels applied to every d2k resource. Keeps
// kubectl filtering and ownership inspection simple.
func commonLabels() map[string]string {
	return map[string]string{
		"app":                          AppLabel,
		"app.kubernetes.io/name":       AppLabel,
		"app.kubernetes.io/managed-by": "kubesolo",
	}
}
