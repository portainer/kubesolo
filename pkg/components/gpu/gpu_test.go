package gpu

import (
	"context"
	"testing"

	"github.com/portainer/kubesolo/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func deploy(t *testing.T, cs *fake.Clientset, vendor string) *appsv1.DaemonSet {
	t.Helper()
	if err := reconcile(context.Background(), cs, vendor); err != nil {
		t.Fatal(err)
	}
	ds, err := cs.AppsV1().DaemonSets(Namespace).Get(context.Background(), plugins[vendor].name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func mounts(c corev1.Container) map[string]corev1.VolumeMount {
	m := map[string]corev1.VolumeMount{}
	for _, vm := range c.VolumeMounts {
		m[vm.MountPath] = vm
	}
	return m
}

func hostPaths(spec corev1.PodSpec) map[string]string {
	m := map[string]string{}
	for _, v := range spec.Volumes {
		m[v.Name] = v.HostPath.Path
	}
	return m
}

// The NVIDIA plugin runs without NVIDIA's container runtime. It loads NVML from
// the host driver root mounted at /driver-root, needs the host's device nodes
// to use it, and writes the CDI spec containerd injects GPUs from.
func TestNVIDIAPluginUsesCDI(t *testing.T) {
	ds := deploy(t, fake.NewSimpleClientset(), types.GPUVendorNVIDIA)
	spec := ds.Spec.Template.Spec
	c := spec.Containers[0]

	if c.Image != types.DefaultNVIDIADevicePluginImage {
		t.Errorf("image = %q", c.Image)
	}
	if c.SecurityContext == nil || !*c.SecurityContext.Privileged {
		t.Error("plugin must be privileged to open /dev/nvidia* without the NVIDIA runtime")
	}
	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	for name, want := range map[string]string{
		"DEVICE_LIST_STRATEGY": "cdi-cri",
		// Jetson: the integrated GPU has no UUID to name its CDI device by,
		"DEVICE_ID_STRATEGY": "index",
		// NVML lives outside the plugin's library search path,
		"LD_LIBRARY_PATH": "/driver-root/usr/lib/aarch64-linux-gnu/nvidia",
		// and JetPack 6's toolkit predates this hook.
		"CDI_DISABLE_HOOKS": "enable-cuda-compat",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}

	m := mounts(c)
	hp := hostPaths(spec)
	for path, host := range map[string]string{
		"/var/lib/kubelet/device-plugins": "/var/lib/kubelet/device-plugins",
		"/driver-root":                    "/",
		"/var/run/cdi":                    "/var/run/cdi",
	} {
		vm, ok := m[path]
		if !ok {
			t.Errorf("no mount at %s", path)
			continue
		}
		if hp[vm.Name] != host {
			t.Errorf("%s mounts host %q, want %q", path, hp[vm.Name], host)
		}
	}
	if !m["/driver-root"].ReadOnly {
		t.Error("the host root must be mounted read-only")
	}
	if spec.PriorityClassName != "system-node-critical" {
		t.Errorf("priority class = %q", spec.PriorityClassName)
	}
}

func TestAMDPlugin(t *testing.T) {
	ds := deploy(t, fake.NewSimpleClientset(), types.GPUVendorAMD)
	spec := ds.Spec.Template.Spec
	c := spec.Containers[0]

	if c.Image != types.DefaultAMDDevicePluginImage {
		t.Errorf("image = %q", c.Image)
	}
	if c.SecurityContext == nil || !*c.SecurityContext.Privileged {
		t.Error("plugin must be privileged")
	}
	m := mounts(c)
	hp := hostPaths(spec)
	for _, path := range []string{"/var/lib/kubelet/device-plugins", "/sys"} {
		if vm, ok := m[path]; !ok || hp[vm.Name] != path {
			t.Errorf("no mount of host %s", path)
		}
	}
}

// Changing gpu.vendor must not leave the previous vendor's plugin running.
func TestSwitchingVendorRemovesThePreviousPlugin(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deploy(t, cs, types.GPUVendorNVIDIA)
	deploy(t, cs, types.GPUVendorAMD)

	_, err := cs.AppsV1().DaemonSets(Namespace).Get(context.Background(), plugins[types.GPUVendorNVIDIA].name, metav1.GetOptions{})
	if !errors.IsNotFound(err) {
		t.Errorf("nvidia plugin still present: %v", err)
	}
}

func TestRemoveDeletesNamespace(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deploy(t, cs, types.GPUVendorNVIDIA)

	if err := remove(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Namespaces().Get(context.Background(), Namespace, metav1.GetOptions{}); !errors.IsNotFound(err) {
		t.Errorf("namespace still present: %v", err)
	}
	if err := remove(context.Background(), cs); err != nil {
		t.Errorf("removing again: %v", err)
	}
}

func TestResourceName(t *testing.T) {
	if got := ResourceName(types.GPUVendorNVIDIA); got != "nvidia.com/gpu" {
		t.Errorf("nvidia: %q", got)
	}
	if got := ResourceName(types.GPUVendorAMD); got != "amd.com/gpu" {
		t.Errorf("amd: %q", got)
	}
}
