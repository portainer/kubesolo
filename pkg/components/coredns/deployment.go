package coredns

import (
	"context"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

func createDeployment(ctx context.Context, clientset *kubernetes.Clientset) error {
	replicas := int32(1)
	priorityClassName := "system-cluster-critical"

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSDeploymentName,
			Namespace: coreDNSNamespace,
			Labels: map[string]string{
				"k8s-app":            "coredns",
				"kubernetes.io/name": "CoreDNS",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 1,
					},
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"k8s-app": "coredns",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"k8s-app": "coredns",
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName:  priorityClassName,
					ServiceAccountName: coreDNSServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:            "coredns",
							Image:           types.DefaultCoreDNSImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: kubesolokubernetes.ParseResourceQuantity("28Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    kubesolokubernetes.ParseResourceQuantity("50m"),
									corev1.ResourceMemory: kubesolokubernetes.ParseResourceQuantity("20Mi"),
								},
							},
							Args: []string{"-conf", "/etc/coredns/Corefile"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config-volume",
									MountPath: "/etc/coredns",
								},
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 53,
									Name:          "dns",
									Protocol:      corev1.ProtocolUDP,
								},
								{
									ContainerPort: 53,
									Name:          "dns-tcp",
									Protocol:      corev1.ProtocolTCP,
								},
								{
									ContainerPort: 8080,
									Name:          "metrics",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/health",
										Port:   intstr.FromInt(8080),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 60,
								TimeoutSeconds:      5,
								PeriodSeconds:       10,
								SuccessThreshold:    1,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/ready",
										Port:   intstr.FromInt(8181),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 10,
								TimeoutSeconds:      5,
								PeriodSeconds:       10,
								SuccessThreshold:    1,
								FailureThreshold:    3,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: coreDNSConfigMapName,
									},
									Items: []corev1.KeyToPath{
										{
											Key:  "Corefile",
											Path: "Corefile",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(coreDNSNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.AppsV1().Deployments(coreDNSNamespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}
