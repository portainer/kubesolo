// Package process handles stopping running KubeSolo processes and releasing
// the ports and file handles they hold before a fresh installation begins.
// All detection is performed by reading /proc directly — no external tools
// (lsof, ss, netstat) are required.
package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/rs/zerolog/log"
)

// KubeSoloPorts are the TCP ports KubeSolo opens; processes holding any of
// these are candidates for termination if they are a KubeSolo process.
var KubeSoloPorts = []int{2379, 6443, 10443, 6060}

// StopAll gracefully stops all running KubeSolo processes: first via the
// installed service manager (if available), then by PID. After SIGTERM it
// waits up to ~15 s before sending SIGKILL, and then checks port holders.
func StopAll(initBinary string) {
	StopViaInitSystem(initBinary)
	StopByExecutablePath()
	StopPortHolders()
	cleanupPIDFile()
}

// StopViaInitSystem asks the detected init system to stop the KubeSolo service
// before resorting to raw process signals. initBinary is the full path to the
// init system's control command (e.g. "/bin/systemctl") or empty if unknown.
func StopViaInitSystem(initBinary string) {
	if initBinary == "" {
		return
	}
	base := filepath.Base(initBinary)
	var args []string
	switch base {
	case "systemctl":
		args = []string{"stop", "kubesolo"}
	case "rc-service":
		args = []string{"kubesolo", "stop"}
	case "service":
		args = []string{"kubesolo", "stop"}
	default:
		return
	}
	log.Info().Msgf("stopping KubeSolo via %s...", base)
	if err := runCommand(initBinary, args...); err != nil {
		log.Debug().Err(err).Msgf("%s stop returned non-zero (may already be stopped)", base)
	}
	time.Sleep(2 * time.Second)
}

// StopByExecutablePath finds all processes whose /proc/<pid>/exe resolves to the
// KubeSolo binary path and sends SIGTERM. After a grace period it sends SIGKILL
// to any survivors. The current process is never signalled.
func StopByExecutablePath() {
	pids := findKubeSoloPIDs()
	if len(pids) == 0 {
		log.Debug().Msg("no running KubeSolo processes found")
		return
	}

	log.Info().Msgf("stopping %d KubeSolo process(es) via SIGTERM...", len(pids))
	for _, pid := range pids {
		signalPID(pid, syscall.SIGTERM)
	}

	// Grace period: up to 7 seconds with exponential back-off
	backoff := time.Second
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(backoff)
		backoff = min(backoff*2, 4*time.Second)
		if remaining := findKubeSoloPIDs(); len(remaining) == 0 {
			log.Info().Msg("KubeSolo processes stopped cleanly")
			return
		}
	}

	// Force-kill stragglers
	remaining := findKubeSoloPIDs()
	if len(remaining) > 0 {
		log.Warn().Msgf("force-killing %d KubeSolo process(es) with SIGKILL...", len(remaining))
		for _, pid := range remaining {
			signalPID(pid, syscall.SIGKILL)
		}
		time.Sleep(time.Second)
	}

	if still := findKubeSoloPIDs(); len(still) > 0 {
		log.Warn().Msgf("some KubeSolo processes may still be running (%v) — continuing anyway", still)
	} else {
		log.Info().Msg("KubeSolo processes stopped")
	}
}

// StopPortHolders finds processes that hold any KubeSolo port and, if they are
// confirmed to be KubeSolo processes (by /proc/<pid>/exe), terminates them.
// Non-KubeSolo processes on the same ports are logged but not touched.
func StopPortHolders() {
	portOwners := findPortOwners()
	if len(portOwners) == 0 {
		log.Debug().Msg("no processes found on KubeSolo ports")
		return
	}

	var kubesoloPIDs []int
	for _, pid := range portOwners {
		if isKubeSoloPID(pid) {
			kubesoloPIDs = append(kubesoloPIDs, pid)
		} else {
			log.Info().Msgf("port in use by non-KubeSolo process (PID %d) — leaving it alone", pid)
		}
	}

	if len(kubesoloPIDs) == 0 {
		return
	}

	log.Info().Msgf("stopping %d KubeSolo process(es) holding ports...", len(kubesoloPIDs))
	for _, pid := range kubesoloPIDs {
		signalPID(pid, syscall.SIGTERM)
	}
	time.Sleep(2 * time.Second)

	// Force-kill if still alive
	for _, pid := range kubesoloPIDs {
		if processAlive(pid) {
			signalPID(pid, syscall.SIGKILL)
		}
	}
}

// CleanupFileConflicts removes socket files and stale PID files left behind by
// a previous KubeSolo installation so they do not block the new binary.
func CleanupFileConflicts(dataPath string) {
	socketPaths := []string{
		filepath.Join(dataPath, "containerd", "containerd.sock"),
		filepath.Join(dataPath, "kine", "socket"),
		"/run/containerd/containerd.sock",
	}
	for _, s := range socketPaths {
		if fi, err := os.Stat(s); err == nil && fi.Mode()&os.ModeSocket != 0 {
			log.Debug().Msgf("removing stale socket: %s", s)
			_ = os.Remove(s)
		}
	}
	cleanupPIDFile()
}

// ── internal helpers ──────────────────────────────────────────────────────────

// findKubeSoloPIDs returns PIDs whose /proc/<pid>/exe resolves to config.DefaultInstallPath,
// excluding the current process.
func findKubeSoloPIDs() []int {
	self := os.Getpid()
	entries, err := filepath.Glob("/proc/[0-9]*/exe")
	if err != nil {
		return nil
	}
	var pids []int
	for _, exeLink := range entries {
		target, err := os.Readlink(exeLink)
		if err != nil || target != config.DefaultInstallPath {
			continue
		}
		pid := pidFromProcPath(exeLink)
		if pid > 0 && pid != self {
			pids = append(pids, pid)
		}
	}
	return pids
}

// findPortOwners reads /proc/net/tcp and /proc/net/tcp6 to find the inodes
// listening on any KubeSolo port, then maps those inodes back to PIDs via
// each process's fd directory. This avoids any dependency on lsof or ss.
func findPortOwners() []int {
	targetPorts := make(map[int]bool, len(KubeSoloPorts))
	for _, p := range KubeSoloPorts {
		targetPorts[p] = true
	}

	inodes := listeningInodes(targetPorts)
	if len(inodes) == 0 {
		return nil
	}
	return inodesToPIDs(inodes)
}

// listeningInodes reads /proc/net/tcp[6] and returns the set of socket inodes
// that are in LISTEN state on any of the target ports.
func listeningInodes(targetPorts map[int]bool) map[uint64]bool {
	inodes := make(map[uint64]bool)
	for _, netFile := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(netFile)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n")[1:] { // skip header
			fields := strings.Fields(line)
			// fields: sl local_addr rem_addr state tx:rx te retransmit uid timeout inode
			if len(fields) < 10 {
				continue
			}
			// state == 0A is TCP_LISTEN
			if fields[3] != "0A" {
				continue
			}
			// local_addr is hex "XXXXXXXX:PPPP" (little-endian IP:port)
			parts := strings.SplitN(fields[1], ":", 2)
			if len(parts) != 2 {
				continue
			}
			portVal, err := strconv.ParseInt(parts[1], 16, 32)
			if err != nil || !targetPorts[int(portVal)] {
				continue
			}
			inode, err := strconv.ParseUint(fields[9], 10, 64)
			if err != nil {
				continue
			}
			inodes[inode] = true
		}
	}
	return inodes
}

// inodesToPIDs maps socket inodes back to PIDs by reading /proc/<pid>/fd/
// symlinks and checking which ones resolve to "socket:[<inode>]".
func inodesToPIDs(targetInodes map[uint64]bool) []int {
	self := os.Getpid()
	pidDirs, _ := filepath.Glob("/proc/[0-9]*/fd")

	seen := make(map[int]bool)
	var pids []int
	for _, fdDir := range pidDirs {
		pid := pidFromProcPath(fdDir)
		if pid <= 0 || pid == self || seen[pid] {
			continue
		}
		fdEntries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fdEntries {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			// Format: "socket:[12345]"
			if !strings.HasPrefix(link, "socket:[") {
				continue
			}
			inodeStr := strings.TrimPrefix(link, "socket:[")
			inodeStr = strings.TrimSuffix(inodeStr, "]")
			inode, err := strconv.ParseUint(inodeStr, 10, 64)
			if err != nil || !targetInodes[inode] {
				continue
			}
			seen[pid] = true
			pids = append(pids, pid)
			break
		}
	}
	return pids
}

// isKubeSoloPID returns true if /proc/<pid>/exe resolves to the KubeSolo binary.
func isKubeSoloPID(pid int) bool {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	return err == nil && target == config.DefaultInstallPath
}

// processAlive returns true if the process with the given PID is still running.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

// signalPID sends sig to pid, logging failures at debug level.
func signalPID(pid int, sig syscall.Signal) {
	if err := syscall.Kill(pid, sig); err != nil {
		log.Debug().Err(err).Msgf("failed to send %s to PID %d", sig, pid)
	}
}

// pidFromProcPath extracts the numeric PID from a /proc/<pid>/... path.
func pidFromProcPath(path string) int {
	// Strip /proc/ prefix and take the first path component
	trimmed := strings.TrimPrefix(path, "/proc/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 {
		return -1
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		return -1
	}
	return pid
}

// cleanupPIDFile removes a stale /var/run/kubesolo.pid if the process it
// references is no longer running.
func cleanupPIDFile() {
	data, err := os.ReadFile(config.PIDFile)
	if err != nil {
		return // no PID file, nothing to do
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		if err := os.Remove(config.PIDFile); err != nil {
			log.Debug().Err(err).Msgf("failed to remove malformed PID file %s", config.PIDFile)
		}
		return
	}
	if !processAlive(pid) {
		log.Debug().Msgf("removing stale PID file (PID %d no longer running)", pid)
		if err := os.Remove(config.PIDFile); err != nil {
			log.Debug().Err(err).Msgf("failed to remove stale PID file %s", config.PIDFile)
		}
	}
}

// runCommand executes a subprocess and discards its output.
func runCommand(name string, args ...string) error {
	cmd := newCommand(name, args...)
	return cmd.Run()
}

