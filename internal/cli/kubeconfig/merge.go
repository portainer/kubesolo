package kubeconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"sigs.k8s.io/yaml"
)

// The kubeconfig is edited as a plain document rather than through kubectl, so
// that merging works on a machine without kubectl, and rather than through
// client-go's typed API, which kubesoloctl does not otherwise link. Fields this
// code does not touch, including ones it does not know, are written back as
// they were read.

// userKubeconfigPath is the kubeconfig a merge writes to: the first file in
// $KUBECONFIG when it is set, as kubectl and kind do, and ~/.kube/config of
// the real user otherwise.
func userKubeconfigPath(realHome string) string {
	if list := filepath.SplitList(os.Getenv("KUBECONFIG")); len(list) > 0 && list[0] != "" {
		return list[0]
	}
	return filepath.Join(realHome, ".kube", "config")
}

// UserConfigPath is the kubeconfig install and fetch merge into.
func UserConfigPath() string {
	_, realHome, _, _ := resolveRealUser()
	return userKubeconfigPath(realHome)
}

// mergeInto merges the kubeconfig src into the file at path, creating it if
// needed. Entries of src replace entries of the same name, everything else in
// the file is kept, and src's current context becomes the current one. A
// non-empty server replaces the server of src's clusters. An existing file is
// backed up first. uid and gid, when positive, become the owner of the file.
func mergeInto(path string, src []byte, server string, uid, gid int) error {
	in, err := parseDoc(src)
	if err != nil {
		return fmt.Errorf("invalid KubeSolo kubeconfig: %w", err)
	}
	if server != "" {
		for _, c := range namedList(in, "clusters") {
			cluster, _ := c["cluster"].(map[string]any)
			if cluster == nil {
				cluster = map[string]any{}
				c["cluster"] = cluster
			}
			cluster["server"] = server
		}
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out, err := parseDoc(existing)
	if err != nil {
		return fmt.Errorf("cannot parse %s: %w", path, err)
	}

	for _, key := range []string{"clusters", "users", "contexts"} {
		for _, e := range namedList(in, key) {
			upsert(out, key, e)
		}
	}
	if cc, ok := in["current-context"].(string); ok && cc != "" {
		out["current-context"] = cc
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		chown(dir, uid, gid)
	}
	if existing != nil {
		backup := path + ".backup-" + time.Now().Format("20060102150405")
		if err := os.WriteFile(backup, existing, 0o600); err != nil {
			return fmt.Errorf("failed to back up %s: %w", path, err)
		}
		chown(backup, uid, gid)
		log.Info().Msgf("backed up existing kubeconfig to %s", backup)
	}

	if err := writeDoc(path, out); err != nil {
		return err
	}
	chown(path, uid, gid)
	log.Info().Msgf("kubeconfig merged successfully: %s", path)
	return nil
}

// removeEntries deletes the named clusters, users and contexts from the file
// at path, and unsets the current context if it was one of them. It reports
// whether anything was removed; a missing file is not an error.
func removeEntries(path string, clusters, users, contexts []string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	doc, err := parseDoc(data)
	if err != nil {
		return false, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	removed := false
	for key, list := range map[string][]string{"clusters": clusters, "users": users, "contexts": contexts} {
		for _, name := range list {
			if remove(doc, key, name) {
				removed = true
			}
		}
	}
	if !removed {
		return false, nil
	}
	if cc, _ := doc["current-context"].(string); cc != "" {
		for _, name := range contexts {
			if cc == name {
				delete(doc, "current-context")
			}
		}
	}
	return true, writeDoc(path, doc)
}

func parseDoc(data []byte) (map[string]any, error) {
	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["apiVersion"]; !ok {
		doc["apiVersion"] = "v1"
	}
	if _, ok := doc["kind"]; !ok {
		doc["kind"] = "Config"
	}
	return doc, nil
}

// writeDoc replaces path atomically, readable by its owner only. A file that
// exists keeps its owner: kubesoloctl runs under sudo, and the replacement would
// otherwise belong to root and lock the user out of their own kubeconfig.
func writeDoc(path string, doc map[string]any) error {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	existing, statErr := os.Stat(path)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if statErr == nil {
		if st, ok := existing.Sys().(*syscall.Stat_t); ok {
			chown(path, int(st.Uid), int(st.Gid))
		}
	}
	return nil
}

// namedList returns the entries of one of a kubeconfig's named lists.
func namedList(doc map[string]any, key string) []map[string]any {
	raw, _ := doc[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func find(doc map[string]any, key, name string) map[string]any {
	for _, e := range namedList(doc, key) {
		if e["name"] == name {
			return e
		}
	}
	return nil
}

func upsert(doc map[string]any, key string, entry map[string]any) {
	raw, _ := doc[key].([]any)
	for i, e := range raw {
		if m, ok := e.(map[string]any); ok && m["name"] == entry["name"] {
			raw[i] = entry
			return
		}
	}
	doc[key] = append(raw, entry)
}

func remove(doc map[string]any, key, name string) bool {
	raw, _ := doc[key].([]any)
	for i, e := range raw {
		if m, ok := e.(map[string]any); ok && m["name"] == name {
			doc[key] = append(raw[:i:i], raw[i+1:]...)
			return true
		}
	}
	return false
}

func chown(path string, uid, gid int) {
	if uid <= 0 {
		return
	}
	if err := os.Lchown(path, uid, gid); err != nil {
		log.Warn().Err(err).Msgf("could not set the owner of %s", path)
	}
}
