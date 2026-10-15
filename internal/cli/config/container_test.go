package config

import (
	"strings"
	"testing"

	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
)

func TestContainerIgnoredPathsExist(t *testing.T) {
	for _, p := range ContainerIgnored {
		if _, ok := kubesoloconfig.FieldByPath(p); !ok {
			t.Errorf("%s is not a KubeSolo setting", p)
		}
	}
}

func TestContainerConfigDropsIrrelevantSettings(t *testing.T) {
	c := Config{
		Path:             "/data/ks",
		NodeIP:           "192.168.1.50",
		MTU:              "abc", // never parsed: it is dropped first
		CPUManagerPolicy: "static",
		ReservedCPUs:     "0",
		Debug:            true,
		D2K:              true,
		D2KNamespace:     "d2k",
		SystemReserved:   "memory=200Mi",
	}
	doc, kept, dropped, _, err := ContainerConfig(c.Flags())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dropped, " "); got != "--path --node-ip --mtu --cpu-manager-policy --reserved-cpus" {
		t.Errorf("dropped = %s", got)
	}
	for _, a := range kept {
		if strings.HasPrefix(a, "--mtu") || strings.HasPrefix(a, "--path") || strings.HasPrefix(a, "--node-ip") {
			t.Errorf("kept an ignored flag: %s", a)
		}
	}
	if doc.Network.MTU != 0 || doc.Network.NodeIP != "" || doc.Path != DefaultPath {
		t.Errorf("ignored settings reached the document: mtu=%d nodeIP=%q path=%q", doc.Network.MTU, doc.Network.NodeIP, doc.Path)
	}
	if !doc.Logging.Debug || !doc.D2K.Enabled || doc.Kubernetes.Kubelet.SystemReserved["memory"] != "200Mi" {
		t.Errorf("relevant settings lost: %+v", doc)
	}
}

func TestContainerConfigDefaultPathIsNotReported(t *testing.T) {
	c := Config{Path: DefaultPath}
	_, _, dropped, _, err := ContainerConfig(c.Flags())
	if err != nil || len(dropped) != 0 {
		t.Errorf("dropped = %v, err = %v", dropped, err)
	}
}

func TestContainerConfigStillValidatesTheRest(t *testing.T) {
	c := Config{Path: DefaultPath, SystemReserved: "bogus"}
	if _, _, _, _, err := ContainerConfig(c.Flags()); err == nil {
		t.Error("expected an error for an invalid --system-reserved")
	}
}

// An existing container's command line, as an upgrade migrates it.
func TestContainerConfigFromAContainerCommandLine(t *testing.T) {
	doc, _, dropped, _, err := ContainerConfig([]string{"--path=/var/lib/kubesolo", "--mtu=1400", "--d2k", "--d2k-namespace=x", "--full", "--container-mode"})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.D2K.Enabled || doc.D2K.Namespace != "x" || doc.Network.MTU != 0 {
		t.Errorf("doc = %+v", doc.D2K)
	}
	if got := strings.Join(dropped, " "); got != "--mtu --container-mode" {
		t.Errorf("dropped = %s", got)
	}
}
