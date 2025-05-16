package apiserver

import (
	"context"
	"fmt"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/rs/zerolog/log"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// applyAPIServerRBAC applies the necessary RBAC rules for API server to access kubelet
func (s *service) applyAPIServerRBAC() error {
	clientset, err := s.initializeKubernetesClient()
	if err != nil {
		return err
	}

	if err := s.verifyAuthentication(clientset); err != nil {
		return err
	}

	if err := s.applyAPIServerToKubeletRBAC(clientset); err != nil {
		return err
	}

	if err := s.applyAdminRBAC(clientset); err != nil {
		return err
	}
	return nil
}

// initializeKubernetesClient creates and returns a Kubernetes client
func (s *service) initializeKubernetesClient() (*kubernetes.Clientset, error) {
	clientset, err := kubesolokubernetes.GetKubernetesClient(s.adminKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}
	return clientset, nil
}

// verifyAuthentication tests if the client can authenticate with the cluster
func (s *service) verifyAuthentication(clientset *kubernetes.Clientset) error {
	_, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Error().Str("component", "apiserver").Str("configPath", s.adminKubeconfig).Msgf("authentication test failed: %v", err)
		return fmt.Errorf("authentication test failed: %v", err)
	}
	return nil
}

// applyAPIServerToKubeletRBAC creates the necessary RBAC rules for API server to access kubelet
func (s *service) applyAPIServerToKubeletRBAC(clientset *kubernetes.Clientset) error {
	apiServerRole := s.createAPIServerRole()
	apiServerRoleBinding := s.createAPIServerRoleBinding()

	if err := s.createOrUpdateClusterRole(clientset, apiServerRole); err != nil {
		return err
	}

	if err := s.createOrUpdateClusterRoleBinding(clientset, apiServerRoleBinding); err != nil {
		return err
	}

	return nil
}

// createAPIServerRole creates the ClusterRole for API server to kubelet access
func (s *service) createAPIServerRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:kube-apiserver-to-kubelet",
			Annotations: map[string]string{
				"rbac.authorization.kubernetes.io/autoupdate": "true",
			},
			Labels: map[string]string{
				"kubernetes.io/bootstrapping": "rbac-defaults",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{
					"nodes/proxy",
					"nodes/stats",
					"nodes/log",
					"nodes/spec",
					"nodes/metrics",
					"pods/exec",
					"pods/portforward",
					"pods/log",
					"pods/attach",
				},
				Verbs: []string{"*"},
			},
		},
	}
}

// createAPIServerRoleBinding creates the ClusterRoleBinding for API server
func (s *service) createAPIServerRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:kube-apiserver",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:kube-apiserver-to-kubelet",
		},
		Subjects: []rbacv1.Subject{
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "User",
				Name:     "kubernetes",
			},
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "User",
				Name:     "kube-apiserver",
			},
		},
	}
}

// applyAdminRBAC creates the admin ClusterRoleBinding
func (s *service) applyAdminRBAC(clientset *kubernetes.Clientset) error {
	adminRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubernetes-admin-cluster-admin",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind: "User",
				Name: "kubernetes-admin",
			},
		},
	}

	return s.createOrUpdateClusterRoleBinding(clientset, adminRoleBinding)
}

// createOrUpdateClusterRole creates or updates a ClusterRole
func (s *service) createOrUpdateClusterRole(clientset *kubernetes.Clientset, role *rbacv1.ClusterRole) error {
	ctx := context.Background()
	_, err := clientset.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{})
	if err != nil {
		_, err = clientset.RbacV1().ClusterRoles().Update(ctx, role, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ClusterRole: %v", err)
		}
	}
	return nil
}

// createOrUpdateClusterRoleBinding creates or updates a ClusterRoleBinding
func (s *service) createOrUpdateClusterRoleBinding(clientset *kubernetes.Clientset, binding *rbacv1.ClusterRoleBinding) error {
	ctx := context.Background()
	_, err := clientset.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{})
	if err != nil {
		_, err = clientset.RbacV1().ClusterRoleBindings().Update(ctx, binding, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ClusterRoleBinding: %v", err)
		}
	}
	return nil
}
