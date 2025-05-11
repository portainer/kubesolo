package kubelet

import (
	"context"
	"fmt"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// applyKubeletRBAC applies the necessary RBAC rules for kubelet
func (s *service) applyKubeletRBAC() error {
	clientset, err := kubesolokubernetes.GetKubernetesClient(s.adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := s.createKubeletClusterRole(clientset); err != nil {
		return err
	}

	if err := s.createKubeletClusterRoleBinding(clientset); err != nil {
		return err
	}

	return nil
}

// createKubeletClusterRole creates the ClusterRole for kubelet
func (s *service) createKubeletClusterRole(clientset *kubernetes.Clientset) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:node",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"nodes/status"},
				Verbs:     []string{"patch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"nodes/proxy"},
				Verbs:     []string{"*"},
			},
		},
	}

	ctx := context.Background()
	_, err := clientset.RbacV1().ClusterRoles().Create(ctx, clusterRole, metav1.CreateOptions{})
	if err != nil {
		_, err = clientset.RbacV1().ClusterRoles().Update(ctx, clusterRole, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ClusterRole: %v", err)
		}
	}
	return nil
}

// createKubeletClusterRoleBinding creates the ClusterRoleBinding for kubelet
func (s *service) createKubeletClusterRoleBinding(clientset *kubernetes.Clientset) error {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:node",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:node",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind: "User",
				Name: fmt.Sprintf("system:node:%s", s.nodeName),
			},
		},
	}

	ctx := context.Background()
	_, err := clientset.RbacV1().ClusterRoleBindings().Create(ctx, clusterRoleBinding, metav1.CreateOptions{})
	if err != nil {
		_, err = clientset.RbacV1().ClusterRoleBindings().Update(ctx, clusterRoleBinding, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ClusterRoleBinding: %v", err)
		}
	}
	return nil
}
