package gpu

import (
	"context"
	"fmt"
	"strconv"

	"github.com/portainer/kubesolo/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// devicePluginDir is where the kubelet listens for device plugins. It is fixed
// by the device plugin API, whatever the kubelet's --root-dir.
const devicePluginDir = "/var/lib/kubelet/device-plugins"

// createDaemonSet reconciles the vendor's device plugin DaemonSet.
func createDaemonSet(ctx context.Context, clientset kubernetes.Interface, vendor string, replicas int) error {
	p := plugins[vendor]
	privileged := true
	hostPath := func(name, path string) corev1.Volume {
		return corev1.Volume{
			Name:         name,
			VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: path}},
		}
	}

	container := corev1.Container{
		Name:            p.name,
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "device-plugins", MountPath: devicePluginDir},
		},
	}
	volumes := []corev1.Volume{hostPath("device-plugins", devicePluginDir)}

	switch vendor {
	case types.GPUVendorNVIDIA:
		// GPUs reach containers through CDI rather than NVIDIA's container
		// runtime, which wraps runc and KubeSolo runs crun. With cdi-cri the
		// plugin writes a CDI spec to /var/run/cdi, where containerd reads it,
		// and hands the kubelet CDI device names to pass through the CRI.
		//
		// The plugin builds the spec, and loads NVML, from the host's driver
		// root, which it expects at /driver-root. It is privileged because no
		// runtime injects /dev/nvidia* into its own container, and NVML needs
		// them.
		container.Image = types.DefaultNVIDIADevicePluginImage
		container.Command = []string{"nvidia-device-plugin"}
		//
		// The rest is for Jetson, and harmless on a discrete GPU. JetPack keeps
		// libnvidia-ml.so.1 in a directory the plugin does not search. The
		// integrated GPU has no UUID, so CDI devices are named by index. And the
		// spec must not use the enable-cuda-compat hook, which the host's
		// nvidia-ctk runs and JetPack 6's toolkit (1.16) does not have.
		container.Env = []corev1.EnvVar{
			{Name: "DEVICE_LIST_STRATEGY", Value: "cdi-cri"},
			{Name: "DEVICE_ID_STRATEGY", Value: "index"},
			{Name: "LD_LIBRARY_PATH", Value: "/driver-root/usr/lib/aarch64-linux-gnu/nvidia"},
			{Name: "CDI_DISABLE_HOOKS", Value: "enable-cuda-compat"},
		}
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{Name: "driver-root", MountPath: "/driver-root", ReadOnly: true},
			corev1.VolumeMount{Name: "cdi-root", MountPath: "/var/run/cdi"},
		)
		volumes = append(volumes, hostPath("driver-root", "/"), hostPath("cdi-root", "/var/run/cdi"))

		if replicas > 1 {
			container.Env = append(container.Env, corev1.EnvVar{Name: "CONFIG_FILE", Value: sharingMountPath + "/" + sharingConfigFile})
			container.VolumeMounts = append(container.VolumeMounts,
				corev1.VolumeMount{Name: "sharing", MountPath: sharingMountPath, ReadOnly: true})
			volumes = append(volumes, corev1.Volume{Name: "sharing", VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: sharingConfigMap}}}})
		}

	case types.GPUVendorAMD:
		// As in AMD's own manifest: the plugin finds GPUs through /sys and
		// passes /dev/kfd and /dev/dri to the containers that request one.
		container.Image = types.DefaultAMDDevicePluginImage
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{Name: "sys", MountPath: "/sys"},
		)
		volumes = append(volumes, hostPath("sys", "/sys"))
	}

	labels := map[string]string{
		"app":                          p.name,
		"app.kubernetes.io/name":       p.name,
		"app.kubernetes.io/managed-by": "kubesolo",
	}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.name,
			Namespace: Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": p.name},
			},
			Template: corev1.PodTemplateSpec{
				// The plugin reads its configuration once, at start: a change
				// to the sharing rolls it.
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: map[string]string{sharingAnnotation: strconv.Itoa(max(replicas, 1))}},
				Spec: corev1.PodSpec{
					PriorityClassName: "system-node-critical",
					Containers:        []corev1.Container{container},
					Volumes:           volumes,
				},
			},
		},
	}

	_, err := clientset.AppsV1().DaemonSets(Namespace).Create(ctx, ds, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.AppsV1().DaemonSets(Namespace).Update(ctx, ds, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

// GPU sharing, for NVIDIA: time-slicing advertises each GPU as several, which
// the pods that request one take turns on.
const (
	sharingConfigMap  = "nvidia-device-plugin-sharing"
	sharingConfigFile = "config.yaml"
	sharingMountPath  = "/etc/nvidia-device-plugin"
	sharingAnnotation = "kubesolo.io/gpu-sharing-replicas"
)

// reconcileSharing writes the device plugin's time-slicing configuration
// when replicas is more than 1, and removes it otherwise. A pod asking for
// more than one share is refused, rather than given two turns on one GPU.
func reconcileSharing(ctx context.Context, clientset kubernetes.Interface, vendor string, replicas int) error {
	cms := clientset.CoreV1().ConfigMaps(Namespace)
	if vendor != types.GPUVendorNVIDIA || replicas <= 1 {
		if err := cms.Delete(ctx, sharingConfigMap, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: sharingConfigMap, Namespace: Namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "kubesolo"}},
		Data: map[string]string{sharingConfigFile: fmt.Sprintf(`version: v1
sharing:
  timeSlicing:
    failRequestsGreaterThanOne: true
    resources:
    - name: nvidia.com/gpu
      replicas: %d
`, replicas)},
	}
	_, err := cms.Create(ctx, cm, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		_, err = cms.Update(ctx, cm, metav1.UpdateOptions{})
	}
	return err
}
