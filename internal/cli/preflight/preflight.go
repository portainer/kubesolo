// Package preflight implements all pre-installation validation checks.
// Each check is an independent function that returns a descriptive error if
// the check fails, making them individually testable and easy to extend.
package preflight

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
)

// Check represents a single pre-flight validation step.
type Check struct {
	Name string
	Run  func() error
}

// Suite returns the ordered list of pre-flight checks to run before installation.
// installPrereqs controls whether missing OS packages are installed automatically
// (e.g. nftables on Alpine Linux). pprofServer mirrors the --pprof-server flag;
// when true, port 6060 is added to the port availability check.
func Suite(installPrereqs, pprofServer bool) []Check {
	return []Check{
		{Name: "root privileges", Run: CheckRoot},
		{Name: "hostname RFC 1123 compliance", Run: CheckHostname},
		{Name: "Docker conflict", Run: CheckDockerConflict},
		{Name: "iptables xt_comment module", Run: CheckIptablesComment},
		{Name: "nftables and iptables", Run: func() error { return CheckAlpineNetworking(installPrereqs) }},
		{Name: "cgroups controllers", Run: func() error { return CheckCgroups(installPrereqs) }},
		{Name: "required ports available", Run: func() error { return CheckPorts(pprofServer) }},
	}
}

// RunSuite executes every check in suite, stopping on the first failure.
// Each passing check emits one detail line; the caller owns the enclosing
// step/ok Printer calls.
func RunSuite(checks []Check) error {
	for _, c := range checks {
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		log.Info().Msgf("%s", c.Name)
	}
	return nil
}

// CheckRoot verifies the installer is running as the root user (UID 0).
func CheckRoot() error {
	if os.Getuid() != 0 {
		return fmt.Errorf("installer must be run as root (use sudo)")
	}
	return nil
}

// hostnameLabelRE matches a single RFC 1123 label: lowercase alphanumerics and
// hyphens, starting and ending with an alphanumeric rune. Labels are validated
// individually so that empty labels (consecutive/leading/trailing dots) and
// over-length labels are rejected — a whole-string regex cannot express the
// per-label "non-empty, ≤63 chars" rules that Kubernetes node names require.
var hostnameLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// CheckHostname verifies that the machine's hostname is RFC 1123 compliant.
// Kubernetes uses the hostname as the Node name, so a non-compliant hostname
// will prevent the kubelet from registering.
func CheckHostname() error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("could not determine hostname: %w", err)
	}
	return validateHostname(hostname)
}

// validateHostname checks a hostname string against RFC 1123 rules.
// Extracted from CheckHostname so tests can exercise the validation logic
// directly without depending on the host's actual hostname.
func validateHostname(hostname string) error {
	if hostname == "" {
		return fmt.Errorf("hostname is empty — please configure a hostname before installing")
	}
	if len(hostname) > 253 {
		return fmt.Errorf("hostname %q is %d characters long; maximum allowed by RFC 1123 is 253", hostname, len(hostname))
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" {
			return fmt.Errorf(
				"hostname %q is not RFC 1123 compliant: it contains an empty label "+
					"(a leading, trailing, or consecutive dot). Please rename the host before installing KubeSolo",
				hostname,
			)
		}
		if len(label) > 63 {
			return fmt.Errorf(
				"hostname %q is not RFC 1123 compliant: label %q exceeds the 63-character maximum. "+
					"Please rename the host before installing KubeSolo",
				hostname, label,
			)
		}
		if !hostnameLabelRE.MatchString(label) {
			return fmt.Errorf(
				"hostname %q is not RFC 1123 compliant: each dot-separated label must contain only "+
					"lowercase letters, numbers and hyphens, and must start and end with an alphanumeric "+
					"character. Please rename the host before installing KubeSolo",
				hostname,
			)
		}
	}
	return nil
}

// CheckDockerConflict ensures Docker is not installed or running.
// Docker's network bridge and iptables rules conflict with KubeSolo's CNI.
func CheckDockerConflict() error {
	// Socket is the most reliable runtime indicator — present even without the CLI
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		return fmt.Errorf(
			"detected Docker daemon socket at /var/run/docker.sock; " +
				"Docker conflicts with KubeSolo networking. " +
				"Please stop and remove Docker before installing: https://docs.kubesolo.io/prerequisites",
		)
	}
	// Check for the docker binary as a secondary signal
	for _, p := range []string{"/usr/bin/docker", "/usr/local/bin/docker"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf(
				"found Docker installed at %s; "+
					"Docker conflicts with KubeSolo networking. "+
					"Please remove Docker before installing: https://docs.kubesolo.io/prerequisites",
				p,
			)
		}
	}
	return nil
}

// CheckIptablesComment verifies that the kernel's xt_comment netfilter module
// is available, which KubeSolo's networking stack requires.
func CheckIptablesComment() error {
	// Try 1: check whether the module is listed in /proc/modules (already loaded)
	if loaded, _ := moduleLoaded("xt_comment"); loaded {
		return nil
	}
	// Try 2: read the available netfilter match names from /proc/net
	if data, err := os.ReadFile("/proc/net/ip_tables_matches"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "comment" {
				return nil
			}
		}
	}
	// Try 3: look for the module file on disk under /lib/modules (may not be loaded yet)
	kr, _ := kernelRelease()
	if kr != "" {
		suffixes := []string{"", ".xz", ".zst", ".gz"}
		for _, sfx := range suffixes {
			candidate := fmt.Sprintf("/lib/modules/%s/kernel/net/netfilter/xt_comment.ko%s", kr, sfx)
			if _, err := os.Stat(candidate); err == nil {
				return nil
			}
		}
		// Broader glob in case of non-standard layout
		if matches, _ := filepath.Glob(fmt.Sprintf("/lib/modules/%s/*/xt_comment.ko*", kr)); len(matches) > 0 {
			return nil
		}
	}
	return fmt.Errorf(
		"iptables xt_comment kernel module is not available. " +
			"KubeSolo's networking requires this module. " +
			"Please ensure your kernel includes xt_comment support, or install the iptables-mod-extra package",
	)
}

// CheckAlpineNetworking verifies that the networking tools required by
// KubeSolo's kube-proxy are present on Alpine Linux.
//
// On Alpine, iptables-legacy kernel modules are absent so kube-proxy uses
// nftables mode. It still invokes the `iptables` binary (which Alpine's
// iptables package provides as an iptables-nft wrapper) as well as `nft`
// directly. Both packages must be installed. On all other distros this
// check is a no-op.
func CheckAlpineNetworking(installPrereqs bool) error {
	if !isAlpine() {
		return nil
	}

	nftPresent := false
	for _, p := range []string{"/usr/sbin/nft", "/sbin/nft", "/usr/bin/nft"} {
		if _, err := os.Stat(p); err == nil {
			nftPresent = true
			break
		}
	}

	iptPresent := false
	for _, p := range []string{"/sbin/iptables", "/usr/sbin/iptables", "/bin/iptables", "/usr/bin/iptables"} {
		if _, err := os.Stat(p); err == nil {
			iptPresent = true
			break
		}
	}

	if nftPresent && iptPresent {
		return nil
	}

	if installPrereqs {
		var toInstall []string
		if !nftPresent {
			toInstall = append(toInstall, "nftables")
		}
		if !iptPresent {
			toInstall = append(toInstall, "iptables")
		}
		log.Info().Msgf("installing Alpine networking packages: %s", strings.Join(toInstall, " "))
		args := append([]string{"add", "--no-cache"}, toInstall...)
		if err := runCommand("apk", args...); err != nil {
			return fmt.Errorf(
				"failed to install %s: %w — please run: apk add %s",
				strings.Join(toInstall, " "), err, strings.Join(toInstall, " "),
			)
		}
		log.Info().Msg("Alpine networking packages installed successfully")
		return nil
	}

	var missing []string
	if !nftPresent {
		missing = append(missing, "nftables")
	}
	if !iptPresent {
		missing = append(missing, "iptables")
	}
	return fmt.Errorf(
		"required Alpine networking packages not found: %s. "+
			"kube-proxy needs both nftables (nft) and iptables (iptables-nft wrapper). "+
			"Run the installer with --install-prereqs to install them automatically, "+
			"or run: apk add %s",
		strings.Join(missing, ", "),
		strings.Join(missing, " "),
	)
}

// requiredControllers is the set of cgroup controllers KubeSolo needs.
var requiredControllers = []string{"cpuset", "cpu", "io", "memory", "pids"}

// CheckCgroups verifies that all required cgroup controllers are available.
// It handles both cgroup v2 (unified hierarchy) and cgroup v1 (per-subsystem mount).
//
// On Alpine with OpenRC, the cgroups init service must be running before
// controllers are visible. This mirrors the bash installer's
// ensure_alpine_cgroups_service call, which runs before check_cgroups:
// the service is enabled and started proactively if controllers are absent,
// then the check re-reads to confirm.
func CheckCgroups(installPrereqs bool) error {
	// On Alpine, ensure the cgroups OpenRC service is running before we check
	// controller availability. This is a no-op if controllers are already up.
	if isAlpine() {
		if err := ensureAlpineCgroupsService(installPrereqs); err != nil {
			return err
		}
	}

	// cgroup v2 unified hierarchy
	if data, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		available := strings.Fields(string(data))
		return checkRequiredControllers(available)
	}

	// cgroup v1 / hybrid: check per-subsystem mount directories
	if _, err := os.Stat("/sys/fs/cgroup"); err != nil {
		return fmt.Errorf(
			"cgroups filesystem not found at /sys/fs/cgroup. " +
				"Please enable cgroups in your kernel and add the following to your boot parameters: " +
				"cgroup_enable=cpuset cgroup_enable=cpu cgroup_enable=io cgroup_memory=1 cgroup_enable=pids",
		)
	}
	var available []string
	for _, c := range requiredControllers {
		dirName := c
		if c == "io" {
			// cgroup v1 mounts block I/O as "blkio", not "io"
			dirName = "blkio"
		}
		dir := filepath.Join("/sys/fs/cgroup", dirName)
		if _, err := os.Stat(dir); err == nil {
			available = append(available, c)
		}
	}
	return checkRequiredControllers(available)
}

// CheckPorts verifies that KubeSolo's required ports are not in use.
// We probe via net.Listen rather than shelling out to lsof/ss/netstat.
// pprofServer should match the --pprof-server flag; when true, port 6060 is
// also checked so a conflict is caught at preflight rather than at runtime.
func CheckPorts(pprofServer bool) error {
	type portEntry struct {
		port int
		name string
	}
	entries := []portEntry{
		{2379, "Kine (etcd replacement)"},
		{6443, "Kubernetes API server"},
		{10443, "Webhook server"},
	}
	if pprofServer {
		entries = append(entries, portEntry{6060, "pprof HTTP server"})
	}
	var conflicts []string
	for _, e := range entries {
		addr := fmt.Sprintf(":%d", e.port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			conflicts = append(conflicts, fmt.Sprintf("port %d (%s)", e.port, e.name))
			continue
		}
		_ = ln.Close()
	}
	if len(conflicts) > 0 {
		return fmt.Errorf(
			"the following ports required by KubeSolo are in use: %s. "+
				"Stop the conflicting processes (or run the installer which will stop existing KubeSolo processes automatically)",
			strings.Join(conflicts, ", "),
		)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// isAlpine returns true if the host is running Alpine Linux.
func isAlpine() bool {
	_, err := os.Stat("/etc/alpine-release")
	return err == nil
}

// moduleLoaded reports whether the named kernel module appears in /proc/modules.
func moduleLoaded(name string) (bool, error) {
	f, err := os.Open("/proc/modules")
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 && fields[0] == name {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// kernelRelease returns the running kernel version string from /proc/version.
func kernelRelease() (string, error) {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return "", err
	}
	// Format: "Linux version 5.15.0-... (gcc ...) #1 SMP ..."
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fields[2], nil
	}
	return "", fmt.Errorf("unexpected /proc/version format")
}

// checkRequiredControllers compares available cgroup controllers against the
// required set and returns a descriptive error if any are missing.
func checkRequiredControllers(available []string) error {
	avail := make(map[string]bool, len(available))
	for _, c := range available {
		avail[c] = true
	}
	var missing []string
	for _, c := range requiredControllers {
		if !avail[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"required cgroup controllers are missing: %s (available: [%s]). "+
				"Add the following to your kernel boot parameters: "+
				"cgroup_enable=cpuset cgroup_enable=cpu cgroup_enable=io cgroup_memory=1 cgroup_enable=pids",
			strings.Join(missing, ", "),
			strings.Join(available, ", "),
		)
	}
	return nil
}

// ensureAlpineCgroupsService enables and starts the Alpine OpenRC cgroups
// service, which populates /sys/fs/cgroup/cgroup.controllers on first boot.
// It is safe to call unconditionally on Alpine — it returns immediately if
// controllers are already available (service already running).
func ensureAlpineCgroupsService(installPrereqs bool) error {
	// No-op if controllers are already available
	if data, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		if len(strings.Fields(string(data))) > 0 {
			return nil
		}
	}

	// rc-update/rc-service must be present — not all minimal Alpine images have OpenRC
	if _, err := os.Stat("/sbin/rc-service"); err != nil {
		log.Debug().Msg("rc-service not found — skipping Alpine cgroups service setup")
		return nil
	}

	if !installPrereqs {
		return fmt.Errorf(
			"cgroup controllers are not available on this Alpine system. " +
				"Run: rc-update add cgroups boot && rc-service cgroups start — " +
				"or re-run the installer with --install-prereqs",
		)
	}

	log.Info().Msg("enabling Alpine cgroups service...")
	_ = runCommand("rc-update", "add", "cgroups", "boot") // idempotent — ignore if already added
	if err := runCommand("rc-service", "cgroups", "start"); err != nil {
		return fmt.Errorf(
			"failed to start Alpine cgroups service: %w. "+
				"Run manually: rc-update add cgroups boot && rc-service cgroups start",
			err,
		)
	}
	log.Info().Msg("Alpine cgroups service started")
	return nil
}

// runCommand executes a system command and streams combined output to the logger.
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Debug().Msgf("%s output: %s", name, strings.TrimSpace(string(out)))
	}
	return err
}
