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

// RemoveFromUserConfig surgically removes the named KubeSolo context, cluster,
// and user entries from the real user's ~/.kube/config. name is the instance
// name used during install (default "kubesolo"). It is a no-op if kubectl is
// not installed or if no matching entries are present. It returns true if it
// actually removed entries.
func RemoveFromUserConfig(name string) bool {
	if name == "" {
		name = "kubesolo"
	}
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		log.Info().Msgf("kubectl not found — %q kubeconfig entries not removed; delete them from ~/.kube/config manually if needed", name)
		return false
	}

	_, realHome, _, _ := resolveRealUser()
	kubeconfigFile := filepath.Join(realHome, ".kube", "config")
	if _, err := os.Stat(kubeconfigFile); err != nil {
		log.Debug().Msgf("no kubeconfig at %s — nothing to clean up", kubeconfigFile)
		return false
	}

	env := append(withoutEnv("KUBECONFIG"), "KUBECONFIG="+kubeconfigFile)

	// Check whether the named context actually exists before touching anything.
	out, err := cmdOutput(kubectlPath, env, "config", "get-contexts", "-o", "name")
	if err != nil || !strings.Contains(out, name) {
		log.Debug().Msgf("no %s context found in kubeconfig — nothing to remove", name)
		return false
	}

	log.Info().Msgf("removing %s entries from %s...", name, kubeconfigFile)

	run := func(args ...string) {
		if err := cmdRun(kubectlPath, env, args...); err != nil {
			log.Debug().Err(err).Msgf("kubectl %s returned non-zero (may already be absent)", strings.Join(args, " "))
		}
	}
	run("config", "delete-context", "kubernetes-admin@"+name)
	run("config", "delete-context", "admin-token@"+name)
	run("config", "unset", "clusters."+name)
	run("config", "unset", "users.kubernetes-admin")
	run("config", "unset", "users.admin-token")

	log.Info().Msgf("%s kubeconfig entries removed", name)

	// Inform about any backup the install step created so the user can restore it.
	backups, _ := filepath.Glob(filepath.Join(realHome, ".kube", "config.backup-*"))
	if len(backups) > 0 {
		log.Info().Msgf("backup kubeconfig available: %s", backups[len(backups)-1])
	}
	return true
}

// cmdRun executes kubectl with the given env and args, discarding output.
func cmdRun(kubectlPath string, env []string, args ...string) error {
	cmd := exec.Command(kubectlPath, args...)
	cmd.Env = env
	return cmd.Run()
}

// cmdOutput executes kubectl and returns combined stdout as a string.
func cmdOutput(kubectlPath string, env []string, args ...string) (string, error) {
	cmd := exec.Command(kubectlPath, args...)
	cmd.Env = env
	out, err := cmd.Output()
	return string(out), err
}

// kubeconfigWaitTimeout bounds how long MergeAfterStartup waits for KubeSolo to
// generate its admin kubeconfig on first boot. A fresh cluster on a constrained
// host has to start containerd + kine + apiserver and generate the PKI before
// the kubeconfig appears, which can take well over 30s — hence the generous
// ceiling. Callers that hit it surface a clear "run kubeconfig fetch" hint.
const kubeconfigWaitTimeout = 120 * time.Second

// MergeAfterStartup waits for KubeSolo to generate its admin kubeconfig and
// then merges it into the real user's ~/.kube/config. It returns an error if
// kubectl is missing or the kubeconfig never appears, so the caller can report
// the true outcome instead of claiming success.
func MergeAfterStartup(dataPath string) error {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found — install kubectl, then run: kubesoloctl kubeconfig fetch (kubeconfig at %s)", KubeSoloKubeconfigPath(dataPath))
	}
	log.Info().Msgf("detected kubectl at %s", kubectlPath)

	ksKubeconfig := KubeSoloKubeconfigPath(dataPath)
	if !waitForFile(ksKubeconfig, kubeconfigWaitTimeout) {
		return fmt.Errorf("timed out after %s waiting for %s — the cluster may still be starting", kubeconfigWaitTimeout.Round(time.Second), ksKubeconfig)
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

	mergeIntoUserConfig(kubectlPath, realUser, realHome, realUID, realGID, ksKubeconfig)

	// Force-patch the CA cert after the merge so a reinstall's new CA always
	// overwrites any stale cert the merge may have preserved from a prior install.
	caPath := filepath.Join(dataPath, "pki", "ca", "ca.crt")
	if _, err := os.Stat(caPath); err == nil {
		patchCACert(kubectlPath, filepath.Join(realHome, ".kube", "config"), "kubesolo", caPath)
	}

	log.Info().Msgf("kubeconfig also accessible at: %s", ksKubeconfig)
	return nil
}

// MergeFromDisk merges the admin kubeconfig from dataPath into the real user's
// ~/.kube/config immediately (no wait loop). Returns an error if the kubeconfig
// file does not exist or kubectl is not installed. Use this for on-demand
// operations like `kubesoloctl kubeconfig fetch` on Linux where KubeSolo is
// already running as a system service.
func MergeFromDisk(dataPath string) error {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found — install kubectl, then run: kubesoloctl kubeconfig fetch")
	}

	ksKubeconfig := KubeSoloKubeconfigPath(dataPath)
	if _, err := os.Stat(ksKubeconfig); err != nil {
		return fmt.Errorf("kubeconfig not found at %s — is KubeSolo running?", ksKubeconfig)
	}

	realUser, realHome, realUID, realGID := resolveRealUser()
	log.Info().Msgf("merging kubeconfig for user %s (home: %s)...", realUser, realHome)

	if realUID > 0 {
		if err := os.Chown(ksKubeconfig, realUID, realGID); err != nil {
			log.Warn().Err(err).Msgf("could not chown source kubeconfig to %s", realUser)
		}
	}

	mergeIntoUserConfig(kubectlPath, realUser, realHome, realUID, realGID, ksKubeconfig)

	caPath := filepath.Join(dataPath, "pki", "ca", "ca.crt")
	if _, err := os.Stat(caPath); err == nil {
		patchCACert(kubectlPath, filepath.Join(realHome, ".kube", "config"), "kubesolo", caPath)
	}
	return nil
}

// mergeIntoUserConfig merges newKubeconfigPath into the real user's
// ~/.kube/config using `kubectl config view --flatten`. It creates the .kube
// directory if needed, backs up any existing config, and corrects ownership of
// the entire ~/.kube tree so the real user owns the result.
func mergeIntoUserConfig(kubectlPath, realUser, realHome string, realUID, realGID int, newKubeconfigPath string) {
	dotKube := filepath.Join(realHome, ".kube")
	if err := os.MkdirAll(dotKube, 0o700); err != nil {
		log.Warn().Err(err).Msgf("failed to create %s", dotKube)
		return
	}

	existingConfig := filepath.Join(dotKube, "config")
	if _, err := os.Stat(existingConfig); err == nil {
		// Copy (not rename) so existingConfig stays in place for the merge step.
		backup := existingConfig + ".backup-" + time.Now().Format("20060102150405")
		if err := copyFile(existingConfig, backup); err != nil {
			log.Warn().Err(err).Msgf("failed to back up existing kubeconfig to %s", backup)
		} else {
			log.Info().Msgf("backed up existing kubeconfig to %s", backup)
		}
	}

	mergedTemp := existingConfig + ".tmp"
	// New config first so its CA cert / credentials win over any stale existing entry.
	mergeEnv := append(withoutEnv("KUBECONFIG"), fmt.Sprintf("KUBECONFIG=%s:%s", newKubeconfigPath, existingConfig))
	cmd := exec.Command(kubectlPath, "config", "view", "--flatten")
	cmd.Env = mergeEnv

	out, err := cmd.Output()
	if err != nil {
		log.Warn().Err(err).Msg("kubectl config view failed — copying KubeSolo config as default")
		if err2 := copyFile(newKubeconfigPath, existingConfig); err2 != nil {
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

	if realUID > 0 {
		if err := chownRecursive(dotKube, realUID, realGID); err != nil {
			log.Warn().Err(err).Msgf("could not fix kubeconfig ownership for %s", realUser)
		}
	}

	log.Info().Msgf("kubeconfig merged successfully: %s", existingConfig)
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

// withoutEnv returns a copy of os.Environ() with any entries whose key matches
// one of the given names removed. Use it before appending overrides so that the
// new value is the only occurrence — on Linux execve uses the first match, so a
// duplicate appended at the end would silently be ignored.
func withoutEnv(keys ...string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, e := range env {
		keep := true
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}

// patchCACert embeds the CA cert at caPath into the named cluster entry of
// kubeconfigFile. Called after mergeIntoUserConfig to ensure a fresh install's
// CA cert always overwrites any stale cert that the merge may have preserved
// from a previous installation.
func patchCACert(kubectlPath, kubeconfigFile, clusterName, caPath string) {
	cmd := exec.Command(kubectlPath, "config", "set-cluster", clusterName,
		"--certificate-authority="+caPath,
		"--embed-certs=true",
		"--kubeconfig="+kubeconfigFile)
	cmd.Env = append(withoutEnv("KUBECONFIG"), "KUBECONFIG="+kubeconfigFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Debug().Msgf("CA cert patch: %v: %s", err, strings.TrimSpace(string(out)))
	} else {
		log.Debug().Msg("CA cert refreshed in merged kubeconfig")
	}
}
