package portainer

import (
	"context"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func createDeployment(ctx context.Context, clientset *kubernetes.Clientset, config types.EdgeAgentConfig) error {
	replicas := int32(1)

	envVars := []corev1.EnvVar{
		{
			Name:  "LOG_LEVEL",
			Value: "INFO",
		},
		{
			Name:  "EDGE",
			Value: "1",
		},
		{
			Name:  "AGENT_CLUSTER_ADDR",
			Value: PortainerEdgeAgentServiceName,
		},
		{
			Name: "KUBERNETES_POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		{
			Name: "EDGE_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: PortainerEdgeAgentSecretName,
					},
					Key: "edge.key",
				},
			},
		},
	}

	if config.EdgeSecret != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name: "AGENT_SECRET",
			ValueFrom: &corev1.EnvVarSource{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: PortainerEdgeAgentConfigMapName,
					},
					Key:      "EDGE_SECRET",
					Optional: kubesolokubernetes.BoolPtr(true),
				},
			},
		})
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PortainerEdgeAgentDeploymentName,
			Namespace: PortainerNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": PortainerEdgeAgentDeploymentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": PortainerEdgeAgentDeploymentName,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: PortainerEdgeAgentServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:            "portainer-agent",
							Image:           types.DefaultPortainerAgentImage,
							ImagePullPolicy: corev1.PullAlways,
							Env:             envVars,
							EnvFrom: []corev1.EnvFromSource{
								{
									ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: PortainerEdgeAgentConfigMapName,
										},
									},
								},
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 9001,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									ContainerPort: 80,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(PortainerNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.AppsV1().Deployments(PortainerNamespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}
