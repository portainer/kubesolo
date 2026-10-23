package gpu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// checkPod is the pod Verify runs on the GPU.
const checkPod = "gpu-check"

// ErrInUse means every GPU, or every share of one, is held by a pod, so the
// check could not run: it says nothing of whether the GPU works.
var ErrInUse = errors.New("every GPU is in use, so it could not be checked")

// verifyTiming bounds Verify's waits.
type verifyTiming struct {
	advertise, pod, poll time.Duration
}

var defaultTiming = verifyTiming{advertise: 5 * time.Minute, pod: 3 * time.Minute, poll: 5 * time.Second}

// Verify proves the GPU usable rather than assuming it: the node advertises
// it, and, for NVIDIA, a pod that requests one runs nvidia-smi on it. The pod
// uses the device plugin's image, already pulled, and CDI puts nvidia-smi in
// it from the host's driver. It returns what the check found.
func Verify(ctx context.Context, adminKubeconfig, nodeName, vendor string) (string, error) {
	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return "", err
	}
	return verify(ctx, clientset, nodeName, vendor, defaultTiming)
}

func verify(ctx context.Context, cs kubernetes.Interface, nodeName, vendor string, timing verifyTiming) (string, error) {
	resource := ResourceName(vendor)
	count, err := waitAdvertised(ctx, cs, nodeName, resource, timing)
	if err != nil {
		return "", err
	}
	if vendor != types.GPUVendorNVIDIA {
		// AMD's plugin puts no tool in a container to run.
		return fmt.Sprintf("%s: %d", resource, count), nil
	}

	pods := cs.CoreV1().Pods(Namespace)
	_ = pods.Delete(ctx, checkPod, metav1.DeleteOptions{})
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: checkPod, Namespace: Namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "kubesolo"}},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:      "check",
				Image:     types.DefaultNVIDIADevicePluginImage,
				Command:   []string{"nvidia-smi", "-L"},
				Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{resource: apiresource.MustParse("1")}},
			}},
		},
	}
	if _, err := pods.Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("start the GPU check: %w", err)
	}
	defer func() { _ = pods.Delete(context.WithoutCancel(ctx), checkPod, metav1.DeleteOptions{}) }()

	deadline := time.Now().Add(timing.pod)
	for {
		p, err := pods.Get(ctx, checkPod, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return strings.TrimSpace(podLogs(ctx, cs)), nil
		case corev1.PodFailed:
			if p.Status.Reason == "UnexpectedAdmissionError" {
				return "", fmt.Errorf("%w: %s", ErrInUse, p.Status.Message)
			}
			return "", fmt.Errorf("the GPU check failed: %s %s %s", p.Status.Reason, p.Status.Message, strings.TrimSpace(podLogs(ctx, cs)))
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("the GPU check did not finish within %s", timing.pod)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(timing.poll):
		}
	}
}

// waitAdvertised waits for the node to advertise the resource, and returns
// how many.
func waitAdvertised(ctx context.Context, cs kubernetes.Interface, nodeName string, resource corev1.ResourceName, timing verifyTiming) (int64, error) {
	deadline := time.Now().Add(timing.advertise)
	for {
		node, err := cs.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return 0, err
		}
		if err == nil {
			if q, ok := node.Status.Allocatable[resource]; ok && q.Value() > 0 {
				return q.Value(), nil
			}
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("%s is not advertised on the node after %s: see the device plugin's logs, kubectl -n %s logs daemonset/%s", resource, timing.advertise, Namespace, plugins[vendorOf(resource)].name)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(timing.poll):
		}
	}
}

func vendorOf(resource corev1.ResourceName) string {
	for vendor, p := range plugins {
		if p.resource == resource {
			return vendor
		}
	}
	return ""
}

func podLogs(ctx context.Context, cs kubernetes.Interface) string {
	stream, err := cs.CoreV1().Pods(Namespace).GetLogs(checkPod, &corev1.PodLogOptions{}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = stream.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(stream, 4096))
	return string(raw)
}
