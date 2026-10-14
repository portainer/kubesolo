package kubeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const kubesoloKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: kubesolo
  cluster:
    server: https://10.0.0.5:6443
    certificate-authority-data: Q0E=
users:
- name: kubernetes-admin
  user:
    client-certificate-data: Q0VSVA==
    client-key-data: S0VZ
contexts:
- name: kubernetes-admin@kubesolo
  context:
    cluster: kubesolo
    user: kubernetes-admin
current-context: kubernetes-admin@kubesolo
`

const otherKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: kind-dev
  cluster:
    server: https://127.0.0.1:40000
users:
- name: kind-dev
  user:
    token: KEEP-ME
contexts:
- name: kind-dev
  context:
    cluster: kind-dev
    user: kind-dev
current-context: kind-dev
`

func readDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func names(doc map[string]any, key string) []string {
	var out []string
	for _, e := range namedList(doc, key) {
		out = append(out, e["name"].(string))
	}
	return out
}

func TestMergeIntoNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config")

	if err := mergeInto(path, []byte(kubesoloKubeconfig), "", -1, -1); err != nil {
		t.Fatal(err)
	}

	doc := readDoc(t, path)
	if got := doc["current-context"]; got != "kubernetes-admin@kubesolo" {
		t.Errorf("current-context = %v", got)
	}
	if got := strings.Join(names(doc, "clusters"), ","); got != "kubesolo" {
		t.Errorf("clusters = %s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestMergeKeepsOtherEntriesAndReplacesOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(otherKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	// A stale entry of the same name, as a previous install leaves behind.
	if err := mergeInto(path, []byte(strings.ReplaceAll(kubesoloKubeconfig, "Q0E=", "U1RBTEU=")), "", -1, -1); err != nil {
		t.Fatal(err)
	}

	if err := mergeInto(path, []byte(kubesoloKubeconfig), "https://127.0.0.1:41234", -1, -1); err != nil {
		t.Fatal(err)
	}

	doc := readDoc(t, path)
	if got := strings.Join(names(doc, "clusters"), ","); got != "kind-dev,kubesolo" {
		t.Errorf("clusters = %s", got)
	}
	if got := strings.Join(names(doc, "users"), ","); got != "kind-dev,kubernetes-admin" {
		t.Errorf("users = %s", got)
	}
	ks := find(doc, "clusters", "kubesolo")["cluster"].(map[string]any)
	if ks["server"] != "https://127.0.0.1:41234" {
		t.Errorf("server = %v", ks["server"])
	}
	if ks["certificate-authority-data"] != "Q0E=" {
		t.Errorf("CA not replaced: %v", ks["certificate-authority-data"])
	}
	if tok := find(doc, "users", "kind-dev")["user"].(map[string]any)["token"]; tok != "KEEP-ME" {
		t.Errorf("unrelated user changed: %v", tok)
	}
	if doc["current-context"] != "kubernetes-admin@kubesolo" {
		t.Errorf("current-context = %v", doc["current-context"])
	}
}

func TestMergeBacksUpExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(otherKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeInto(path, []byte(kubesoloKubeconfig), "", -1, -1); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "config.backup-*"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); string(data) != otherKubeconfig {
		t.Error("backup does not hold the previous file")
	}
}

func TestRemoveEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(otherKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeInto(path, []byte(kubesoloKubeconfig), "", -1, -1); err != nil {
		t.Fatal(err)
	}

	removed, err := removeEntries(path, []string{"kubesolo"}, []string{"kubernetes-admin"}, []string{"kubernetes-admin@kubesolo", "admin-token@kubesolo"})
	if err != nil || !removed {
		t.Fatalf("removed = %v, err = %v", removed, err)
	}

	doc := readDoc(t, path)
	if got := strings.Join(names(doc, "clusters"), ","); got != "kind-dev" {
		t.Errorf("clusters = %s", got)
	}
	if got := strings.Join(names(doc, "contexts"), ","); got != "kind-dev" {
		t.Errorf("contexts = %s", got)
	}
	// The current context was removed, so it must not be left dangling.
	if cc, ok := doc["current-context"]; ok && cc != "" {
		t.Errorf("current-context = %v, want unset", cc)
	}

	removed, err = removeEntries(path, []string{"kubesolo"}, nil, nil)
	if err != nil || removed {
		t.Errorf("second removal: removed = %v, err = %v", removed, err)
	}
}

func TestRemoveEntriesMissingFile(t *testing.T) {
	removed, err := removeEntries(filepath.Join(t.TempDir(), "absent"), []string{"kubesolo"}, nil, nil)
	if err != nil || removed {
		t.Errorf("removed = %v, err = %v", removed, err)
	}
}

func TestUserKubeconfigPath(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	if got := userKubeconfigPath("/home/u"); got != "/home/u/.kube/config" {
		t.Errorf("default = %s", got)
	}
	t.Setenv("KUBECONFIG", "/tmp/a.yaml"+string(os.PathListSeparator)+"/tmp/b.yaml")
	if got := userKubeconfigPath("/home/u"); got != "/tmp/a.yaml" {
		t.Errorf("KUBECONFIG = %s, want its first entry", got)
	}
}
