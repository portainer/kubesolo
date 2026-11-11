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
