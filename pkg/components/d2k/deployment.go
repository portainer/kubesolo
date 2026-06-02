package d2k

import (
	"context"
	"strconv"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// createDeployment reconciles the d2k Deployment. The Deployment uses the
// downward API to populate D2K_NAMESPACE, sets D2K_PORT=2376 and
// D2K_SWARM_MODE=true, and mounts the d2k-tls Secret into /etc/d2k/tls so
// d2k listens with TLS on 2376.
func createDeployment(ctx context.Context, clientset *kubernetes.Clientset, namespace, image string) error {
	replicas := int32(1)

	envVars := []corev1.EnvVar{
		{
			Name: "D2K_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name:  "D2K_PORT",
			Value: strconv.Itoa(int(types.DefaultD2KPort)),
		},
		{
			Name:  "D2K_SWARM_MODE",
			Value: "true",
		},
		{
			Name:  "D2K_LOG_LEVEL",
			Value: "info",
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeploymentName,
			Namespace: namespace,
			Labels:    commonLabels(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": AppLabel,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: commonLabels(),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: ServiceAccountName,
					Volumes: []corev1.Volume{
						{
							Name: "tls",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: TLSSecretName,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "d2k",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             envVars,
							Ports: []corev1.ContainerPort{
								{
									Name:          "docker-api",
									ContainerPort: types.DefaultD2KPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "tls",
									MountPath: "/etc/d2k/tls",
									ReadOnly:  true,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    kubesolokubernetes.ParseResourceQuantity("50m"),
									corev1.ResourceMemory: kubesolokubernetes.ParseResourceQuantity("32Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    kubesolokubernetes.ParseResourceQuantity("200m"),
									corev1.ResourceMemory: kubesolokubernetes.ParseResourceQuantity("128Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}
