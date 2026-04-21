// Package kubeconfig merges the KubeSolo admin kubeconfig into the invoking
// user's ~/.kube/config after installation, mirroring the behaviour of the
// original bash installer script.
package kubeconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// KubeSoloKubeconfigPath returns the default location of the admin kubeconfig
// that KubeSolo generates during its first startup.
func KubeSoloKubeconfigPath(dataPath string) string {
	return filepath.Join(dataPath, "pki", "admin", "admin.kubeconfig")
}

// MergeAfterStartup waits up to 30 seconds for KubeSolo to generate its admin
// kubeconfig and then merges it into the real user's ~/.kube/config. It is a
// no-op if kubectl is not installed.
func MergeAfterStartup(dataPath string) {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		log.Info().Msg("kubectl not found — skipping kubeconfig merge")
		log.Info().Msgf("kubeconfig location: %s", KubeSoloKubeconfigPath(dataPath))
		return
	}
	log.Info().Msgf("detected kubectl at %s", kubectlPath)

	ksKubeconfig := KubeSoloKubeconfigPath(dataPath)
	if !waitForFile(ksKubeconfig, 30*time.Second) {
		log.Warn().Msgf("timed out waiting for kubeconfig at %s", ksKubeconfig)
		return
	}

	realUser, realHome, realUID, realGID := resolveRealUser()
	log.Info().Msgf("merging kubeconfig for user %s (home: %s)...", realUser, realHome)

	// Chown the source kubeconfig to the real user so that KUBECONFIG=<path>
	// set in shell profiles (.bashrc etc.) keeps working after installation.
	// The file stays 0600 — it just becomes owned by the real user rather than root.
	if realUID > 0 {
		if err := os.Chown(ksKubeconfig, realUID, realGID); err != nil {
			log.Warn().Err(err).Msgf("could not chown source kubeconfig to %s", realUser)
		} else {
			log.Debug().Msgf("kubeconfig ownership set to %s: %s", realUser, ksKubeconfig)
		}
	}

	dotKube := filepath.Join(realHome, ".kube")
	if err := os.MkdirAll(dotKube, 0o700); err != nil {
		log.Warn().Err(err).Msgf("failed to create %s", dotKube)
		return
	}

	existingConfig := filepath.Join(dotKube, "config")
	if _, err := os.Stat(existingConfig); err == nil {
		backup := existingConfig + ".backup-" + time.Now().Format("20060102150405")
		if err := os.Rename(existingConfig, backup); err != nil {
			log.Warn().Err(err).Msgf("failed to back up existing kubeconfig to %s", backup)
		} else {
			log.Info().Msgf("backed up existing kubeconfig to %s", backup)
		}
	}

	// Merge: KUBECONFIG="existing:new" kubectl config view --flatten > merged
	// kubectl runs as root (current process), but both source files are already
	// readable by root. The merged output is written to a temp file and ownership
	// of the entire ~/.kube tree is corrected to the real user below.
	mergedTemp := existingConfig + ".tmp"
	mergeEnv := append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s:%s", existingConfig, ksKubeconfig))
	cmd := exec.Command(kubectlPath, "config", "view", "--flatten")
	cmd.Env = mergeEnv

	out, err := cmd.Output()
	if err != nil {
		// Fall back to a standalone copy of the KubeSolo config.
		// copyFile preserves the raw content; ownership was already fixed above
		// so the real user can read it even via KUBECONFIG=<ksKubeconfig>.
		log.Warn().Err(err).Msg("kubectl config view failed — copying KubeSolo config as default")
		if err2 := copyFile(ksKubeconfig, existingConfig); err2 != nil {
			log.Warn().Err(err2).Msg("failed to copy KubeSolo kubeconfig")
			return
		}
	} else {
		if err := os.WriteFile(mergedTemp, out, 0o600); err != nil {
			log.Warn().Err(err).Msg("failed to write merged kubeconfig")
			return
		}
		if err := os.Rename(mergedTemp, existingConfig); err != nil {
			log.Warn().Err(err).Msg("failed to move merged kubeconfig into place")
			_ = os.Remove(mergedTemp)
			return
		}
	}

	// Fix ownership of ~/.kube/ so the real user owns everything under it
	if realUID > 0 && realGID > 0 {
		if err := chownRecursive(dotKube, realUID, realGID); err != nil {
			log.Warn().Err(err).Msgf("could not fix kubeconfig ownership for %s", realUser)
		}
	}

	log.Info().Msgf("kubeconfig merged successfully: %s", existingConfig)
	log.Info().Msgf("kubeconfig also accessible at: %s", ksKubeconfig)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// resolveRealUser returns the real user's name, home directory, UID, and GID.
// It tries four strategies in order so that the kubeconfig ends up in the
// invoking user's home directory regardless of how privilege was escalated:
//
//  1. sudo  — SUDO_USER / SUDO_UID / SUDO_GID environment variables
//  2. doas  — DOAS_USER environment variable (set by some doas builds)
//  3. loginuid — /proc/self/loginuid records the UID of the user who
//     originally authenticated; the kernel preserves it across sudo/doas/su
//  4. fallback — current process environment ($HOME / $USER), which will be
//     root when none of the above applies
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

	// 3. /proc/self/loginuid — the kernel sets this to the UID of the user
	// who originally logged in (via PAM) and it is preserved across privilege
	// escalation.  The sentinel value 4294967295 (^uint32(0)) means "not set".
	// Parse as uint64 with a 32-bit cap so the constant is safe on 32-bit
	// architectures (arm) where int overflows at 2147483647.
	if data, err := os.ReadFile("/proc/self/loginuid"); err == nil {
		const unsetLoginUID uint64 = 4294967295
		if loginUID, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32); err == nil &&
			loginUID > 0 && loginUID != unsetLoginUID {
			if e := passwdByUID(int(loginUID)); e != nil && e.name != "root" {
				return e.name, e.home, e.uid, e.gid
			}
		}
	}

	// 4. Fallback: already running as root with no detectable original user
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

// passwdByUID looks up a user by numeric UID in /etc/passwd.
func passwdByUID(uid int) *passwdEntry {
	return scanPasswd(func(e *passwdEntry) bool { return e.uid == uid })
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

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
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
