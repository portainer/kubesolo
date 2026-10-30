package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/types"
	"sigs.k8s.io/yaml"
)

// networksFile, below path, records the cluster networks the cluster was
// created with.
const networksFile = "networks.yaml"

// networks are the immutable settings addresses are allocated from.
type networks struct {
	PodCIDR     string `json:"podCIDR"`
	ServiceCIDR string `json:"serviceCIDR"`
	ClusterDNS  string `json:"clusterDNS"`
}

func networksOf(cfg *types.Config) networks {
	return networks{cfg.Kubernetes.PodCIDR, cfg.Kubernetes.ServiceCIDR, cfg.Kubernetes.ClusterDNS}
}

// CheckNetworks refuses a start whose cluster networks differ from the ones the
// cluster was created with. The config API and kubesoloctl refuse the change,
// but the file can be edited by hand.
//
// Until the cluster's database exists nothing was allocated from them, so they
// are recorded again at each start. A cluster without a record was created
// before they were configurable, with the defaults.
func CheckNetworks(cfg *types.Config) error {
	record := filepath.Join(cfg.Path, networksFile)
	if !clusterExists(cfg.Path) {
		return writeNetworks(record, networksOf(cfg))
	}
	if _, err := os.Stat(record); errors.Is(err, fs.ErrNotExist) {
		if err := writeNetworks(record, networksOf(Defaults())); err != nil {
			return err
		}
	}
	return RefuseNetworkChange(cfg)
}

// RefuseNetworkChange is CheckNetworks without recording anything, for a
// configuration about to be saved.
func RefuseNetworkChange(cfg *types.Config) error {
	if !clusterExists(cfg.Path) {
		return nil
	}
	record := filepath.Join(cfg.Path, networksFile)
	had := networksOf(Defaults())
	if raw, err := os.ReadFile(record); err == nil {
		if err := yaml.UnmarshalStrict(raw, &had); err != nil {
			return fmt.Errorf("read %s: %w", record, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	want := networksOf(cfg)
	for _, n := range []struct{ path, had, want string }{
		{"kubernetes.podCIDR", had.PodCIDR, want.PodCIDR},
		{"kubernetes.serviceCIDR", had.ServiceCIDR, want.ServiceCIDR},
		{"kubernetes.clusterDNS", had.ClusterDNS, want.ClusterDNS},
	} {
		if n.had == n.want {
			continue
		}
		f, _ := FieldByPath(n.path)
		return fmt.Errorf("%s cannot be changed on an existing installation: %s. The cluster was created with %q, recorded in %s; put it back, or install a new cluster with another path",
			n.path, f.Unchangeable, n.had, record)
	}
	return nil
}

// clusterExists says whether the install at path has a database.
func clusterExists(path string) bool {
	_, err := os.Stat(filepath.Join(path, types.KubesoloKineDir, types.DefaultKineDBFile))
	return err == nil
}

func writeNetworks(path string, n networks) error {
	raw, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	return upgrade.WriteFileAtomic(path, raw, 0o600)
}
