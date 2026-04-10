package coredns

import (
	"context"

	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	legacyDeploymentName     = "coredns"
	legacyConfigMapName      = "coredns"
	legacyServiceAccountName = "coredns"
	legacyClusterRoleName    = "system:coredns"
)

// cleanupLegacyResources removes pod-based CoreDNS resources left over from
// older KubeSolo versions. Safe to call on fresh installs — all deletes
// are no-ops when the resources don't exist.
//
// The kube-dns Service is also deleted so it can be recreated without a pod
// selector (the old Service used Selector: {k8s-app: coredns}, which caused
// the endpoint controller to fight the manually managed EndpointSlice).
func cleanupLegacyResources(ctx context.Context, clientset *kubernetes.Clientset) {
	deleteDeployment(ctx, clientset)
	deleteConfigMap(ctx, clientset)
	deleteServiceAccount(ctx, clientset)
	deleteClusterRoleBinding(ctx, clientset)
	deleteClusterRole(ctx, clientset)
	deleteService(ctx, clientset)
}

func deleteDeployment(ctx context.Context, clientset *kubernetes.Clientset) {
	err := clientset.AppsV1().Deployments(coreDNSNamespace).Delete(ctx, legacyDeploymentName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		log.Warn().Str("component", "coredns").Err(err).Msg("failed to delete legacy CoreDNS deployment")
	}
}

func deleteConfigMap(ctx context.Context, clientset *kubernetes.Clientset) {
	err := clientset.CoreV1().ConfigMaps(coreDNSNamespace).Delete(ctx, legacyConfigMapName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		log.Warn().Str("component", "coredns").Err(err).Msg("failed to delete legacy CoreDNS configmap")
	}
}

func deleteServiceAccount(ctx context.Context, clientset *kubernetes.Clientset) {
	err := clientset.CoreV1().ServiceAccounts(coreDNSNamespace).Delete(ctx, legacyServiceAccountName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		log.Warn().Str("component", "coredns").Err(err).Msg("failed to delete legacy CoreDNS service account")
	}
}

func deleteClusterRoleBinding(ctx context.Context, clientset *kubernetes.Clientset) {
	err := clientset.RbacV1().ClusterRoleBindings().Delete(ctx, legacyClusterRoleName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		log.Warn().Str("component", "coredns").Err(err).Msg("failed to delete legacy CoreDNS cluster role binding")
	}
}

func deleteClusterRole(ctx context.Context, clientset *kubernetes.Clientset) {
	err := clientset.RbacV1().ClusterRoles().Delete(ctx, legacyClusterRoleName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		log.Warn().Str("component", "coredns").Err(err).Msg("failed to delete legacy CoreDNS cluster role")
	}
}

func deleteService(ctx context.Context, clientset *kubernetes.Clientset) {
	err := clientset.CoreV1().Services(coreDNSNamespace).Delete(ctx, coreDNSServiceName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		log.Warn().Str("component", "coredns").Err(err).Msg("failed to delete legacy CoreDNS service")
	}
}
