// Package kubeconfig merges the KubeSolo admin kubeconfig into the invoking
// user's ~/.kube/config after installation, mirroring the behaviour of the
// original bash installer script.
package kubeconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// HostCluster is the kubeconfig cluster name of a host install.
const HostCluster = "kubesolo"

// KubeSoloKubeconfigPath returns the default location of the admin kubeconfig
// that KubeSolo generates during its first startup.
func KubeSoloKubeconfigPath(dataPath string) string {
	return filepath.Join(dataPath, "pki", "admin", "admin.kubeconfig")
}

// RemoveFromUserConfig surgically removes the context, cluster and user
// entries of one KubeSolo instance from the real user's kubeconfig. cluster is
// the instance's cluster name: its container name in container mode, and
// "kubesolo" for a host install. It returns true if it actually removed
// entries.
func RemoveFromUserConfig(cluster string) bool {
	_, realHome, _, _ := resolveRealUser()
	kubeconfigFile := userKubeconfigPath(realHome)

	removed, err := removeEntries(kubeconfigFile, entryNames(cluster))
	if err != nil {
		log.Warn().Err(err).Msgf("could not remove %s entries from %s", cluster, kubeconfigFile)
		return false
	}
	if !removed {
		log.Debug().Msgf("no %s entries in %s — nothing to remove", cluster, kubeconfigFile)
		return false
	}
	log.Info().Msgf("%s kubeconfig entries removed from %s", cluster, kubeconfigFile)

	// Inform about any backup the install step created so the user can restore it.
	backups, _ := filepath.Glob(kubeconfigFile + ".backup-*")
	if len(backups) > 0 {
		log.Info().Msgf("backup kubeconfig available: %s", backups[len(backups)-1])
	}
	return true
}

// kubeconfigWaitTimeout bounds how long MergeAfterStartup waits for KubeSolo to
// generate its admin kubeconfig on first boot. A fresh cluster on a constrained
// host has to start containerd + kine + apiserver and generate the PKI before
// the kubeconfig appears, which can take well over 30s — hence the generous
// ceiling. Callers that hit it surface a clear "run kubeconfig fetch" hint.
const kubeconfigWaitTimeout = 120 * time.Second

// MergeAfterStartup waits for KubeSolo to generate its admin kubeconfig and
// then merges it into the real user's kubeconfig. It returns an error if the
// kubeconfig never appears or cannot be merged, so the caller can report the
// true outcome instead of claiming success.
func MergeAfterStartup(dataPath string) error {
	ksKubeconfig := KubeSoloKubeconfigPath(dataPath)
	if !waitForFile(ksKubeconfig, kubeconfigWaitTimeout) {
		return fmt.Errorf("timed out after %s waiting for %s — the cluster may still be starting", kubeconfigWaitTimeout.Round(time.Second), ksKubeconfig)
	}
	return MergeFromDisk(dataPath)
}

// MergeFromDisk merges the admin kubeconfig from dataPath into the real user's
// kubeconfig immediately (no wait loop). Returns an error if the kubeconfig
// file does not exist. Use this for on-demand operations like `kubesoloctl
// kubeconfig fetch` on Linux where KubeSolo is already running as a system
// service.
func MergeFromDisk(dataPath string) error {
	ksKubeconfig := KubeSoloKubeconfigPath(dataPath)
	if _, err := os.Stat(ksKubeconfig); err != nil {
		return fmt.Errorf("kubeconfig not found at %s — is KubeSolo running?", ksKubeconfig)
	}

	realUser, realHome, realUID, realGID := resolveRealUser()
	log.Info().Msgf("merging kubeconfig for user %s (home: %s)...", realUser, realHome)

	// Chown the source kubeconfig to the real user so that KUBECONFIG=<path>
	// set in shell profiles (.bashrc etc.) keeps working after installation.
	// The file stays 0600 — it just becomes owned by the real user rather than root.
	if realUID > 0 {
		if err := os.Chown(ksKubeconfig, realUID, realGID); err != nil {
			log.Warn().Err(err).Msgf("could not chown source kubeconfig to %s", realUser)
		}
	}

	if err := mergeFile(userKubeconfigPath(realHome), ksKubeconfig, HostCluster, realUID, realGID); err != nil {
		return err
	}
	log.Info().Msgf("kubeconfig also accessible at: %s", ksKubeconfig)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// resolveRealUser returns the real user's name, home directory, UID, and GID.
// It tries three strategies in order so that the kubeconfig ends up in the
// invoking user's home directory when privilege was explicitly escalated via
// sudo/doas, and in root's own home otherwise — deliberately not guessing
// based on who originally logged into the session (e.g. via a `sudo su -`
// root shell): if the process is running as root with no explicit sudo/doas
// target, attribution is root, full stop:
//
//  1. sudo  — SUDO_USER / SUDO_UID / SUDO_GID environment variables
//  2. doas  — DOAS_USER environment variable (set by some doas builds)
//  3. fallback — current process environment ($HOME / $USER), which will be
//     root when neither of the above applies
func resolveRealUser() (name, home string, uid, gid int) {
	// 1. sudo
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
		uid, _ = strconv.Atoi(os.Getenv("SUDO_UID"))
		gid, _ = strconv.Atoi(os.Getenv("SUDO_GID"))
		if e := passwdByName(sudoUser); e != nil {
			return e.name, e.home, uid, gid
		}
		return sudoUser, "/home/" + sudoUser, uid, gid
	}

	// 2. doas (DOAS_USER is set by some builds of doas)
	if doasUser := os.Getenv("DOAS_USER"); doasUser != "" && doasUser != "root" {
		if e := passwdByName(doasUser); e != nil {
			return e.name, e.home, e.uid, e.gid
		}
		return doasUser, "/home/" + doasUser, -1, -1
	}

	// 3. Fallback: already running as root (or an unrecognized escalation
	// tool) with no explicit sudo/doas target
	home = os.Getenv("HOME")
	if home == "" {
		home = "/root"
	}
	name = os.Getenv("USER")
	if name == "" {
		name = "root"
	}
	return name, home, -1, -1
}

// passwdEntry holds the fields from a single /etc/passwd line that we need.
type passwdEntry struct {
	name string
	uid  int
	gid  int
	home string
}

// passwdByName looks up a user by name in /etc/passwd.
func passwdByName(username string) *passwdEntry {
	return scanPasswd(func(e *passwdEntry) bool { return e.name == username })
}

// scanPasswd parses /etc/passwd and returns the first entry for which match
// returns true, or nil if no entry matches.
func scanPasswd(match func(*passwdEntry) bool) *passwdEntry {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		// Format: name:password:uid:gid:gecos:home:shell
		fields := strings.SplitN(line, ":", 7)
		if len(fields) < 6 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		gid, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		e := &passwdEntry{name: fields[0], uid: uid, gid: gid, home: fields[5]}
		if match(e) {
			return e
		}
	}
	return nil
}

// waitForFile polls path until it exists or timeout elapses.
func waitForFile(path string, timeout time.Duration) bool {
	log.Info().Msgf("waiting for kubeconfig at %s (up to %s)...", path, timeout)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// chownRecursive changes ownership of path and everything under it.
func chownRecursive(path string, uid, gid int) error {
	return filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}
