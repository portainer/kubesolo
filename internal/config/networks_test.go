package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/types"
)

func withNetworks(path, pod, service string) *types.Config {
	cfg := Defaults()
	cfg.Path = path
	cfg.Kubernetes.PodCIDR, cfg.Kubernetes.ServiceCIDR = pod, service
	return cfg
}

// createCluster gives path a database, as a cluster that was started has.
func createCluster(t *testing.T, path string) {
	t.Helper()
	db := filepath.Join(path, types.KubesoloKineDir, types.DefaultKineDBFile)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Until the cluster exists nothing was allocated from its networks.
func TestCheckNetworksBeforeTheClusterExists(t *testing.T) {
	path := t.TempDir()
	for _, pod := range []string{"10.50.0.0/16", "10.60.0.0/16"} {
		if err := CheckNetworks(withNetworks(path, pod, "10.51.0.0/16")); err != nil {
			t.Fatalf("pod CIDR %s: %v", pod, err)
		}
	}
	createCluster(t, path)
	if err := CheckNetworks(withNetworks(path, "10.60.0.0/16", "10.51.0.0/16")); err != nil {
		t.Errorf("the networks last recorded: %v", err)
	}
}

func TestCheckNetworksRefusesAChange(t *testing.T) {
	path := t.TempDir()
	if err := CheckNetworks(withNetworks(path, "10.50.0.0/16", "10.51.0.0/16")); err != nil {
		t.Fatal(err)
	}
	createCluster(t, path)

	changed := map[string]*types.Config{
		"kubernetes.podCIDR":     withNetworks(path, "10.52.0.0/16", "10.51.0.0/16"),
		"kubernetes.serviceCIDR": withNetworks(path, "10.50.0.0/16", "10.53.0.0/16"),
		"kubernetes.clusterDNS":  withNetworks(path, "10.50.0.0/16", "10.51.0.0/16"),
	}
	changed["kubernetes.clusterDNS"].Kubernetes.ClusterDNS = "10.51.0.53"
	for field, cfg := range changed {
		if err := CheckNetworks(cfg); err == nil || !strings.HasPrefix(err.Error(), field+" cannot be changed") {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

// A cluster without a record was created before the networks were
// configurable, with the defaults.
func TestCheckNetworksTakesAClusterWithoutARecordAsTheDefaults(t *testing.T) {
	path := t.TempDir()
	createCluster(t, path)

	if err := CheckNetworks(withNetworks(path, "10.50.0.0/16", "10.43.0.0/16")); err == nil || !strings.Contains(err.Error(), `created with "10.42.0.0/16"`) {
		t.Errorf("err = %v, want the pod CIDR refused against the default", err)
	}
	if err := CheckNetworks(withNetworks(path, "10.42.0.0/16", "10.43.0.0/16")); err != nil {
		t.Errorf("the defaults: %v", err)
	}
}
