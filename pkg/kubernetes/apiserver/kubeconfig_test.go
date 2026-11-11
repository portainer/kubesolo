package apiserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkAdminKubeconfig(t *testing.T) {
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "admin.kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The parent of the link does not exist yet, as /etc/kubernetes on Alpine.
	link := filepath.Join(dir, "etc", "kubernetes", "admin.conf")

	for range 2 {
		if err := linkAdminKubeconfig(kubeconfig, link); err != nil {
			t.Fatalf("linkAdminKubeconfig: %v", err)
		}
		if got, err := os.Readlink(link); err != nil || got != kubeconfig {
			t.Fatalf("link points to %q (%v), want %q", got, err, kubeconfig)
		}
	}
}

// Tools that import the admin kubeconfig, such as Apple container's
// `container cluster create`, accept only one cluster, user and context.
func TestCreateKubeConfigHasOneClusterUserAndContext(t *testing.T) {
	s := &service{port: 6443}
	cfg := s.createKubeConfig(&certificateData{adminCert: []byte("CERT"), adminKey: []byte("KEY"), ca: []byte("CA")}, "10.0.0.5")

	if len(cfg.Clusters) != 1 || len(cfg.AuthInfos) != 1 || len(cfg.Contexts) != 1 {
		t.Fatalf("clusters = %d, users = %d, contexts = %d, want 1 each", len(cfg.Clusters), len(cfg.AuthInfos), len(cfg.Contexts))
	}
	ctx := cfg.Contexts[cfg.CurrentContext]
	if cfg.CurrentContext != "kubernetes-admin@kubesolo" || ctx == nil || ctx.AuthInfo != "kubernetes-admin" {
		t.Errorf("current-context = %q (%+v)", cfg.CurrentContext, ctx)
	}
}

// A regular admin.conf belongs to someone else, such as kubeadm on the host.
func TestLinkAdminKubeconfigKeepsRegularFile(t *testing.T) {
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "admin.kubeconfig")
	link := filepath.Join(dir, "admin.conf")
	if err := os.WriteFile(link, []byte("owned elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := linkAdminKubeconfig(kubeconfig, link); err == nil {
		t.Fatal("linkAdminKubeconfig replaced a regular file")
	}
	if got, err := os.ReadFile(link); err != nil || string(got) != "owned elsewhere\n" {
		t.Fatalf("admin.conf = %q (%v), want it untouched", got, err)
	}
}

func TestLinkAdminKubeconfigReplacesStaleLink(t *testing.T) {
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "admin.kubeconfig")
	link := filepath.Join(dir, "admin.conf")
	if err := os.Symlink(filepath.Join(dir, "old.kubeconfig"), link); err != nil {
		t.Fatal(err)
	}

	if err := linkAdminKubeconfig(kubeconfig, link); err != nil {
		t.Fatalf("linkAdminKubeconfig: %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != kubeconfig {
		t.Fatalf("link points to %q (%v), want %q", got, err, kubeconfig)
	}
}

// A relative --path would otherwise resolve against the link's directory.
func TestLinkAdminKubeconfigUsesAbsoluteSource(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	link := filepath.Join(dir, "etc", "admin.conf")

	if err := linkAdminKubeconfig(filepath.Join("data", "admin.kubeconfig"), link); err != nil {
		t.Fatalf("linkAdminKubeconfig: %v", err)
	}
	want := filepath.Join(dir, "data", "admin.kubeconfig")
	if got, err := os.Readlink(link); err != nil || got != want {
		t.Fatalf("link points to %q (%v), want %q", got, err, want)
	}
}
