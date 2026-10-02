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
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	bootstraputil "k8s.io/cluster-bootstrap/token/util"
)

const component = "bootstrap"

// managedByLabel marks the token Secrets KubeSolo owns, so that rotating to a
// new token can revoke the previous one without touching Secrets somebody else
// created. Talos seeds its own bootstrap token, and deleting that would break
// the very kubelet this exists to enrol.
const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "kubesolo"
)

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
	// Checked here rather than trusting the caller: a token read from a
	// bootstrap kubeconfig never passes through config validation, and one the
	// authenticator cannot parse would seed a Secret that only ever yields 401s.
	//
	// This is the API server's own validator, which compares the secret half in
	// constant time rather than matching it against a pattern.
	if !bootstraputil.IsValidBootstrapToken(token) {
		return fmt.Errorf("bootstrap token is not in <id>.<secret> form")
	}

	id, secret, _ := strings.Cut(token, ".")

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := applyTokenSecret(clientset, id, secret); err != nil {
		return err
	}

	if err := revokeSupersededTokens(clientset, id); err != nil {
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
			Name:      bootstraputil.BootstrapTokenSecretName(id),
			Namespace: metav1.NamespaceSystem,
			Labels:    map[string]string{managedByLabel: managedByValue},
		},
		Type: bootstrapapi.SecretTypeBootstrapToken,
		StringData: map[string]string{
			bootstrapapi.BootstrapTokenDescriptionKey:      "KubeSolo TLS bootstrap token for a host-managed kubelet",
			bootstrapapi.BootstrapTokenIDKey:               id,
			bootstrapapi.BootstrapTokenSecretKey:           secret,
			bootstrapapi.BootstrapTokenUsageAuthentication: "true",
			bootstrapapi.BootstrapTokenUsageSigningKey:     "true",
			bootstrapapi.BootstrapTokenExtraGroupsKey:      tokenGroup,
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

// revokeSupersededTokens deletes the bootstrap token Secrets KubeSolo created
// for a previous token.
//
// The Secrets carry no expiry — a rebooted appliance has to be able to re-enrol
// its own kubelet with nobody present to mint a new token — so changing the
// configured token would otherwise leave the old one valid forever. Only
// Secrets this package labelled are considered.
func revokeSupersededTokens(clientset *kubernetes.Clientset, keepID string) error {
	ctx := context.Background()
	secrets := clientset.CoreV1().Secrets(metav1.NamespaceSystem)

	// Narrowed by type as well as by label, because this deletes: the label
	// alone would also match anything else of ours that adopts the convention,
	// and a bootstrap token Secret is the only kind that can be revoked this way.
	existing, err := secrets.List(ctx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
		FieldSelector: "type=" + string(bootstrapapi.SecretTypeBootstrapToken),
	})
	if err != nil {
		return fmt.Errorf("failed to list bootstrap token secrets: %v", err)
	}

	for _, secret := range existing.Items {
		if secret.Name == bootstraputil.BootstrapTokenSecretName(keepID) {
			continue
		}

		if err := secrets.Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to revoke superseded bootstrap token secret %s: %v", secret.Name, err)
		}

		log.Info().Str("component", component).Str("secret", secret.Name).
			Msg("revoked a bootstrap token secret superseded by the configured token")
	}

	return nil
}
