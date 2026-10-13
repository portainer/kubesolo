// Package syspath puts the standard system binary directories on PATH.
package syspath

import (
	"os"
	"path/filepath"
	"strings"
)

// systemDirs are the standard directories system tools live in. iptables, nft,
// modprobe, semodule and apparmor_parser are in the sbin ones, which a root
// shell started with su rather than su - leaves off PATH.
var systemDirs = []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// Ensure appends any standard system directory missing from PATH, so tools
// resolve the same way whatever environment started the process. Child
// processes inherit it: containerd, the CNI plugins and kube-proxy, and KubeSolo
// itself when kubesoloctl starts it as a daemon or in the foreground.
func Ensure() {
	_ = os.Setenv("PATH", withSystemDirs(os.Getenv("PATH")))
}

// withSystemDirs returns path with every missing system directory appended, in
// order, after the directories already there.
func withSystemDirs(path string) string {
	present := make(map[string]bool)
	var dirs []string
	for _, dir := range filepath.SplitList(path) {
		if dir != "" {
			present[dir] = true
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range systemDirs {
		if !present[dir] {
			dirs = append(dirs, dir)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}
