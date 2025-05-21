package localpath

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
			Name:      "local-path-provisioner-service-account",
			Namespace: LocalPathNamespace,
		},
	}

	_, err := clientset.CoreV1().ServiceAccounts(LocalPathNamespace).Create(ctx, serviceAccount, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func createRole(ctx context.Context, clientset *kubernetes.Clientset) error {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-path-provisioner-role",
			Namespace: LocalPathNamespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list", "watch", "create", "patch", "update", "delete"},
			},
		},
	}

	_, err := clientset.RbacV1().Roles(LocalPathNamespace).Create(ctx, role, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.RbacV1().Roles(LocalPathNamespace).Update(ctx, role, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func createClusterRole(ctx context.Context, clientset *kubernetes.Clientset) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-path-provisioner-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"nodes", "persistentvolumeclaims", "configmaps", "pods", "pods/log"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs:     []string{"get", "list", "watch", "create", "patch", "update", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"create", "patch"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"storageclasses"},
				Verbs:     []string{"get", "list", "watch"},
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

func createRoleBinding(ctx context.Context, clientset *kubernetes.Clientset) error {
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-path-provisioner-bind",
			Namespace: LocalPathNamespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "local-path-provisioner-role",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "local-path-provisioner-service-account",
				Namespace: LocalPathNamespace,
			},
		},
	}

	_, err := clientset.RbacV1().RoleBindings(LocalPathNamespace).Create(ctx, roleBinding, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.RbacV1().RoleBindings(LocalPathNamespace).Update(ctx, roleBinding, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func createClusterRoleBinding(ctx context.Context, clientset *kubernetes.Clientset) error {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-path-provisioner-bind",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "local-path-provisioner-role",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "local-path-provisioner-service-account",
				Namespace: LocalPathNamespace,
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
