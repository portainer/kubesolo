package coredns

import (
	"context"
	"fmt"

	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	coreDNSNamespace   = "kube-system"
	coreDNSServiceName = "kube-dns"
)

func createService(ctx context.Context, clientset *kubernetes.Clientset, nodeIP string) error {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSServiceName,
			Namespace: coreDNSNamespace,
			Labels: map[string]string{
				"k8s-app":            "kube-dns",
				"kubernetes.io/name": "CoreDNS",
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: types.DefaultCoreDNSIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "dns",
					Port:       53,
					Protocol:   corev1.ProtocolUDP,
					TargetPort: intstr.FromInt32(dnsBoundPort),
				},
				{
					Name:       "dns-tcp",
					Port:       53,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt32(dnsBoundPort),
				},
			},
		},
	}

	_, err := clientset.CoreV1().Services(coreDNSNamespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Validate the existing Service has the expected ClusterIP and is selectorless
			existing, getErr := clientset.CoreV1().Services(coreDNSNamespace).Get(ctx, coreDNSServiceName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("failed to get existing CoreDNS service: %v", getErr)
			}
			if existing.Spec.ClusterIP != types.DefaultCoreDNSIP || len(existing.Spec.Selector) > 0 {
				// Service doesn't match expected spec — delete and recreate
				if delErr := clientset.CoreV1().Services(coreDNSNamespace).Delete(ctx, coreDNSServiceName, metav1.DeleteOptions{}); delErr != nil {
					return fmt.Errorf("failed to delete mismatched CoreDNS service: %v", delErr)
				}
				if _, createErr := clientset.CoreV1().Services(coreDNSNamespace).Create(ctx, service, metav1.CreateOptions{}); createErr != nil {
					return fmt.Errorf("failed to recreate CoreDNS service: %v", createErr)
				}
			}
			return ensureEndpointSlice(ctx, clientset, nodeIP)
		}
		return fmt.Errorf("failed to create CoreDNS service: %v", err)
	}

	return ensureEndpointSlice(ctx, clientset, nodeIP)
}

func ensureEndpointSlice(ctx context.Context, clientset *kubernetes.Clientset, nodeIP string) error {
	dnsPort := int32(dnsBoundPort)
	epSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSServiceName,
			Namespace: coreDNSNamespace,
			Labels: map[string]string{
				"k8s-app":                    "kube-dns",
				discoveryv1.LabelServiceName: coreDNSServiceName,
				discoveryv1.LabelManagedBy:   "kubesolo",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses:  []string{nodeIP},
				Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Name: ptr.To("dns"), Port: &dnsPort, Protocol: ptr.To(corev1.ProtocolUDP)},
			{Name: ptr.To("dns-tcp"), Port: &dnsPort, Protocol: ptr.To(corev1.ProtocolTCP)},
		},
	}

	_, err := clientset.DiscoveryV1().EndpointSlices(coreDNSNamespace).Create(ctx, epSlice, metav1.CreateOptions{})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create CoreDNS EndpointSlice: %v", err)
		}
		// Update existing EndpointSlice to ensure endpoints/ports match current nodeIP
		existing, getErr := clientset.DiscoveryV1().EndpointSlices(coreDNSNamespace).Get(ctx, coreDNSServiceName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing CoreDNS EndpointSlice: %v", getErr)
		}
		epSlice.ObjectMeta.ResourceVersion = existing.ResourceVersion
		if _, updateErr := clientset.DiscoveryV1().EndpointSlices(coreDNSNamespace).Update(ctx, epSlice, metav1.UpdateOptions{}); updateErr != nil {
			return fmt.Errorf("failed to update CoreDNS EndpointSlice: %v", updateErr)
		}
	}

	return nil
}
