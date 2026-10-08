package portainer

import (
	"context"
	"testing"

	"github.com/portainer/kubesolo/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func agentDeployment(t *testing.T, cs *fake.Clientset) *appsv1.Deployment {
	t.Helper()
	d, err := cs.AppsV1().Deployments(PortainerNamespace).Get(context.Background(), PortainerEdgeAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func image(d *appsv1.Deployment) string {
	return d.Spec.Template.Spec.Containers[0].Image
}

// setImage is Portainer updating the agent the way it does: by changing the
// Deployment's image directly.
func setImage(t *testing.T, cs *fake.Clientset, img string) {
	t.Helper()
	d := agentDeployment(t, cs)
	d.Spec.Template.Spec.Containers[0].Image = img
	if _, err := cs.AppsV1().Deployments(PortainerNamespace).Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func deploy(t *testing.T, cs *fake.Clientset, cfg types.EdgeAgentConfig) {
	t.Helper()
	if err := createDeployment(context.Background(), cs, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestAgentImageFollowsConfigurationButNotOverPortainer(t *testing.T) {
	cs := fake.NewSimpleClientset()

	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0"})
	d := agentDeployment(t, cs)
	if image(d) != "portainer/agent:2.30.0" || d.Annotations[ConfiguredImageAnnotation] != "portainer/agent:2.30.0" {
		t.Fatalf("created with %q, annotation %q", image(d), d.Annotations[ConfiguredImageAnnotation])
	}

	// Portainer upgrades the agent; a KubeSolo restart must not undo it.
	setImage(t, cs, "portainer/agent:2.31.0")
	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0"})
	if got := image(agentDeployment(t, cs)); got != "portainer/agent:2.31.0" {
		t.Errorf("restart reverted Portainer's agent upgrade: %q", got)
	}

	// The configuration asks for a new image: it takes effect.
	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.32.0"})
	d = agentDeployment(t, cs)
	if image(d) != "portainer/agent:2.32.0" || d.Annotations[ConfiguredImageAnnotation] != "portainer/agent:2.32.0" {
		t.Errorf("configured image not applied: %q, annotation %q", image(d), d.Annotations[ConfiguredImageAnnotation])
	}
}

// A Deployment created before KubeSolo recorded the configured image is
// adopted as it is: the running image becomes the baseline.
func TestLegacyDeploymentIsAdopted(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0"})
	d := agentDeployment(t, cs)
	delete(d.Annotations, ConfiguredImageAnnotation)
	d.Spec.Template.Spec.Containers[0].Image = "portainer/agent:2.29.0"
	if _, err := cs.AppsV1().Deployments(PortainerNamespace).Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0"})
	d = agentDeployment(t, cs)
	if image(d) != "portainer/agent:2.29.0" {
		t.Errorf("legacy deployment's image changed to %q", image(d))
	}
	if d.Annotations[ConfiguredImageAnnotation] != "portainer/agent:2.30.0" {
		t.Errorf("annotation = %q", d.Annotations[ConfiguredImageAnnotation])
	}
}

func TestDefaultImage(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deploy(t, cs, types.EdgeAgentConfig{})
	if got := image(agentDeployment(t, cs)); got != types.DefaultPortainerEdgeImage {
		t.Errorf("image = %q", got)
	}
}

func hasSocket(d *appsv1.Deployment) (volume, mount, env bool) {
	spec := d.Spec.Template.Spec
	for _, v := range spec.Volumes {
		if v.Name == APISocketVolume && v.HostPath != nil && *v.HostPath.Type == corev1.HostPathDirectoryOrCreate {
			volume = true
		}
	}
	for _, m := range spec.Containers[0].VolumeMounts {
		mount = mount || m.Name == APISocketVolume
	}
	for _, e := range spec.Containers[0].Env {
		env = env || (e.Name == APISocketEnv && e.Value == "/var/lib/kubesolo/agent-api/kubesolo.sock")
	}
	return
}

// The agent gets the API socket's directory while the API is enabled, and loses
// it when the API is turned off, without disturbing anything else.
func TestAPISocketMountFollowsConfiguration(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0"})
	if v, m, e := hasSocket(agentDeployment(t, cs)); v || m || e {
		t.Fatal("socket mounted with the API off")
	}

	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0", APISocketDir: "/var/lib/kubesolo/agent-api"})
	if v, m, e := hasSocket(agentDeployment(t, cs)); !v || !m || !e {
		t.Fatalf("socket not mounted: volume %v mount %v env %v", v, m, e)
	}

	// Applying the same configuration again changes nothing.
	before := agentDeployment(t, cs)
	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0", APISocketDir: "/var/lib/kubesolo/agent-api"})
	after := agentDeployment(t, cs)
	if before.ResourceVersion != after.ResourceVersion {
		t.Error("an unchanged configuration updated the Deployment")
	}
	if n := len(after.Spec.Template.Spec.Volumes); n != 1 {
		t.Errorf("%d volumes after reapplying", n)
	}

	deploy(t, cs, types.EdgeAgentConfig{Image: "portainer/agent:2.30.0"})
	d := agentDeployment(t, cs)
	if v, m, e := hasSocket(d); v || m || e {
		t.Error("socket still mounted after the API was turned off")
	}
	if len(d.Spec.Template.Spec.Containers[0].Env) == 0 {
		t.Error("removing the socket removed the agent's other environment")
	}
}
