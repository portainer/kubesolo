// Package bootstrap seeds what a kubelet KubeSolo does not control needs in
// order to join: the token Secret it authenticates with, and the RBAC that lets
// it request a client certificate and have that request approved without a human.
//
// KubeSolo's own kubelet needs none of this. It is handed a client certificate
// KubeSolo has already signed, so it never authenticates as anything other than
// its final identity. A foreign kubelet has no such option — Talos, for one,
// only ever enrols by presenting a bootstrap token and requesting a certificate.
package bootstrap

import (
	"context"
	"fmt"
	"strings"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const component = "bootstrap"

// tokenGroup is the group the token authenticates into. It is scoped to KubeSolo
// rather than reusing kubeadm's name so that the bindings below cannot silently
// widen an existing kubeadm cluster's bootstrappers.
const tokenGroup = "system:bootstrappers:kubesolo:default-node-token"

// nodesGroup is the group a kubelet lands in once it holds a node certificate,
// which is what makes renewals self-service.
const nodesGroup = "system:nodes"

// Apply creates the bootstrap token Secret and the binding set, and is safe to
// re-run: everything is created or updated in place.
func Apply(adminKubeconfig, token string) error {
	id, secret, found := strings.Cut(token, ".")
	if !found {
		return fmt.Errorf("bootstrap token is not in <id>.<secret> form")
	}

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := applyTokenSecret(clientset, id, secret); err != nil {
		return err
	}

	if err := applyBindings(clientset); err != nil {
		return err
	}

	log.Info().Str("component", component).
		Str("token-id", id).
		Msg("tls bootstrapping is enabled; a kubelet presenting this token can obtain a node certificate")

	return nil
}

// applyTokenSecret writes the Secret the API server's bootstrap authenticator
// reads. The name is not cosmetic: the authenticator looks up
// bootstrap-token-<id> directly from the token presented to it.
//
// No expiration is set. A token that expires leaves a rebooted appliance unable
// to re-enrol its own kubelet, with no operator present to mint another, and it
// matches how Talos treats cluster.token.
func applyTokenSecret(clientset *kubernetes.Clientset, id, secret string) error {
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bootstrap-token-" + id,
			Namespace: metav1.NamespaceSystem,
		},
		Type: corev1.SecretTypeBootstrapToken,
		StringData: map[string]string{
			"description":                    "KubeSolo TLS bootstrap token for a host-managed kubelet",
			"token-id":                       id,
			"token-secret":                   secret,
			"usage-bootstrap-authentication": "true",
			"usage-bootstrap-signing":        "true",
			"auth-extra-groups":              tokenGroup,
		},
	}

	ctx := context.Background()
	secrets := clientset.CoreV1().Secrets(metav1.NamespaceSystem)

	_, err := secrets.Create(ctx, tokenSecret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = secrets.Update(ctx, tokenSecret, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("failed to apply bootstrap token secret: %v", err)
	}

	return nil
}

// applyBindings grants the three permissions an enrolling kubelet needs. Each
// ClusterRole referenced is created by the API server itself, so only the
// bindings are KubeSolo's to make.
func applyBindings(clientset *kubernetes.Clientset) error {
	bindings := []struct {
		name        string
		clusterRole string
		group       string
	}{
		// Create a certificate signing request at all.
		{"kubesolo:kubelet-bootstrap", "system:node-bootstrapper", tokenGroup},
		// Have that request approved without an operator: a single node has
		// nobody to approve it, so an unapproved CSR is a cluster that never starts.
		{"kubesolo:node-autoapprove-bootstrap", "system:certificates.k8s.io:certificatesigningrequests:nodeclient", tokenGroup},
		// Renew it later. Without this the node works until the certificate
		// expires and then silently stops being able to talk to the API server.
		{"kubesolo:node-autoapprove-certificate-rotation", "system:certificates.k8s.io:selfnodeclient", nodesGroup},
	}

	ctx := context.Background()
	clusterRoleBindings := clientset.RbacV1().ClusterRoleBindings()

	for _, b := range bindings {
		binding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: b.name},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     b.clusterRole,
			},
			Subjects: []rbacv1.Subject{{
				APIGroup: rbacv1.GroupName,
				Kind:     rbacv1.GroupKind,
				Name:     b.group,
			}},
		}

		_, err := clusterRoleBindings.Create(ctx, binding, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			_, err = clusterRoleBindings.Update(ctx, binding, metav1.UpdateOptions{})
		}
		if err != nil {
			return fmt.Errorf("failed to apply cluster role binding %s: %v", b.name, err)
		}

		log.Debug().Str("component", component).
			Str("binding", b.name).Str("role", b.clusterRole).Str("group", b.group).
			Msg("applied bootstrap rbac")
	}

	return nil
}
