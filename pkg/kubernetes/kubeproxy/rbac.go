package kubeproxy

import (
	"context"
	"fmt"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// applyKubeProxyRBAC applies the necessary RBAC rules for kube-proxy
func (s *service) applyKubeProxyRBAC() error {
	clientset, err := kubesolokubernetes.GetKubernetesClient(s.adminKubeconfigFile)
	if err != nil {
		return err
	}

	if err := s.createServiceAccount(clientset); err != nil {
		return err
	}

	if err := s.createClusterRoleBinding(clientset); err != nil {
		return err
	}

	return nil
}

// createServiceAccount creates the kube-proxy service account
func (s *service) createServiceAccount(clientset *kubernetes.Clientset) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-proxy",
			Namespace: "kube-system",
			Labels: map[string]string{
				"addonmanager.kubernetes.io/mode": "Reconcile",
			},
		},
	}

	_, err := clientset.CoreV1().ServiceAccounts("kube-system").Create(context.Background(), serviceAccount, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create service account: %v", err)
	}

	return nil
}

// createClusterRoleBinding creates the cluster role binding for kube-proxy
func (s *service) createClusterRoleBinding(clientset *kubernetes.Clientset) error {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:kube-proxy",
			Labels: map[string]string{
				"addonmanager.kubernetes.io/mode": "Reconcile",
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "kube-proxy",
				Namespace: "kube-system",
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "ClusterRole",
			Name:     "system:node-proxier",
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	_, err := clientset.RbacV1().ClusterRoleBindings().Create(context.Background(), clusterRoleBinding, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create cluster role binding: %v", err)
	}

	return nil
}
