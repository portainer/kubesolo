package coredns

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func createServiceAccount(ctx context.Context, clientset *kubernetes.Clientset) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSServiceAccountName,
			Namespace: coreDNSNamespace,
		},
	}

	_, err := clientset.CoreV1().ServiceAccounts(coreDNSNamespace).Create(ctx, serviceAccount, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func createClusterRole(ctx context.Context, clientset *kubernetes.Clientset) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: coreDNSClusterRoleName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"endpoints", "services", "pods", "namespaces"},
				Verbs:     []string{"list", "watch"},
			},
			{
				APIGroups: []string{"discovery.k8s.io"},
				Resources: []string{"endpointslices"},
				Verbs:     []string{"list", "watch"},
			},
		},
	}

	_, err := clientset.RbacV1().ClusterRoles().Create(ctx, clusterRole, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.RbacV1().ClusterRoles().Update(ctx, clusterRole, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func createClusterRoleBinding(ctx context.Context, clientset *kubernetes.Clientset) error {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: coreDNSClusterRoleName,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     coreDNSClusterRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      coreDNSServiceAccountName,
				Namespace: coreDNSNamespace,
			},
		},
	}

	_, err := clientset.RbacV1().ClusterRoleBindings().Create(ctx, clusterRoleBinding, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.RbacV1().ClusterRoleBindings().Update(ctx, clusterRoleBinding, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}
