package portainer

import (
	"context"
	"path/filepath"
	"slices"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// agentContainerName is the agent's container in its Deployment.
const agentContainerName = "portainer-agent"

// AgentSocketName is the API socket's name inside APISocketDir.
const AgentSocketName = "kubesolo.sock"

func createDeployment(ctx context.Context, clientset kubernetes.Interface, config types.EdgeAgentConfig) error {
	replicas := int32(1)

	image := config.Image
	if image == "" {
		image = types.DefaultPortainerEdgeImage
	}

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
							Name:            agentContainerName,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
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

	if config.APISocketDir != "" {
		addAPISocket(&deployment.Spec.Template.Spec, config.APISocketDir)
	}

	return reconcileDeployment(ctx, clientset, deployment, image)
}

// ConfiguredImageAnnotation records, on the agent Deployment, the image
// KubeSolo's configuration last asked for.
//
// Portainer updates the agent by changing the Deployment's image itself, and
// that must survive a KubeSolo restart, so the Deployment is not simply
// overwritten with the configured image. But a change to the configured image
// must take effect too — otherwise the configuration claims one agent version
// while the cluster runs another, and nothing ever reconciles them. The
// annotation tells the two apart: only a configured image that differs from the
// last one applied is a change to make.
const ConfiguredImageAnnotation = "kubesolo.io/configured-image"

// APISocketVolume is the agent pod's volume for the KubeSolo API socket
// directory, mounted at the same path inside the container.
const APISocketVolume = "kubesolo-api"

// APISocketEnv tells the agent where the KubeSolo API socket is.
const APISocketEnv = "KUBESOLO_API_SOCKET"

func reconcileDeployment(ctx context.Context, clientset kubernetes.Interface, desired *appsv1.Deployment, image string) error {
	deployments := clientset.AppsV1().Deployments(PortainerNamespace)
	if desired.Annotations == nil {
		desired.Annotations = map[string]string{}
	}
	desired.Annotations[ConfiguredImageAnnotation] = image

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := deployments.Get(ctx, desired.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			_, err = deployments.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}

		next := existing.DeepCopy()
		if next.Annotations == nil {
			next.Annotations = map[string]string{}
		}
		lastApplied, recorded := next.Annotations[ConfiguredImageAnnotation]
		switch {
		case !recorded:
			// Created by a KubeSolo that did not record the image. Whatever
			// runs now is the baseline; later changes to the configuration
			// are measured from it.
			next.Annotations[ConfiguredImageAnnotation] = image
		case lastApplied != image:
			setAgentImage(&next.Spec.Template.Spec, image)
			next.Annotations[ConfiguredImageAnnotation] = image
		}

		syncAPISocket(&next.Spec.Template.Spec, &desired.Spec.Template.Spec)

		if equality.Semantic.DeepEqual(existing, next) {
			return nil
		}
		_, err = deployments.Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}

func setAgentImage(spec *corev1.PodSpec, image string) {
	for i := range spec.Containers {
		if spec.Containers[i].Name == agentContainerName {
			spec.Containers[i].Image = image
		}
	}
}

// addAPISocket mounts dir, which holds the KubeSolo API socket, into the agent
// container. The directory is mounted, not the socket: KubeSolo recreates the
// socket on every start, and a bind mount of the file would go on pointing at
// the old, deleted one.
func addAPISocket(spec *corev1.PodSpec, dir string) {
	dirType := corev1.HostPathDirectoryOrCreate
	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: APISocketVolume,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: dir, Type: &dirType},
		},
	})
	for i := range spec.Containers {
		if spec.Containers[i].Name != agentContainerName {
			continue
		}
		c := &spec.Containers[i]
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: APISocketVolume, MountPath: dir})
		c.Env = append(c.Env, corev1.EnvVar{Name: APISocketEnv, Value: filepath.Join(dir, AgentSocketName)})
	}
}

// syncAPISocket makes next's API socket volume, mount and environment variable
// match desired's, adding or removing them, and leaves everything else alone.
func syncAPISocket(next, desired *corev1.PodSpec) {
	next.Volumes = slices.DeleteFunc(next.Volumes, func(v corev1.Volume) bool { return v.Name == APISocketVolume })
	for i := range next.Containers {
		c := &next.Containers[i]
		c.VolumeMounts = slices.DeleteFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == APISocketVolume })
		c.Env = slices.DeleteFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == APISocketEnv })
	}

	for _, v := range desired.Volumes {
		if v.Name == APISocketVolume {
			next.Volumes = append(next.Volumes, v)
		}
	}
	for _, dc := range desired.Containers {
		for i := range next.Containers {
			if next.Containers[i].Name != dc.Name {
				continue
			}
			for _, m := range dc.VolumeMounts {
				if m.Name == APISocketVolume {
					next.Containers[i].VolumeMounts = append(next.Containers[i].VolumeMounts, m)
				}
			}
			for _, e := range dc.Env {
				if e.Name == APISocketEnv {
					next.Containers[i].Env = append(next.Containers[i].Env, e)
				}
			}
		}
	}
}

// RunningAgentImage returns the image the agent Deployment runs, or "" when
// there is no agent Deployment.
func RunningAgentImage(ctx context.Context, adminKubeconfig string) (string, error) {
	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return "", err
	}
	d, err := clientset.AppsV1().Deployments(PortainerNamespace).Get(ctx, PortainerEdgeAgentDeploymentName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Name == agentContainerName {
			return c.Image, nil
		}
	}
	return "", nil
}
