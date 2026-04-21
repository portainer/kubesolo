// Package detect identifies the host system's architecture, libc variant,
// init system, and environment type. All detection is performed by reading
// the filesystem and /proc directly — no external commands are invoked,
// keeping the installer binary dependency-free.
package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// InitSystem represents the process supervision system running on the host.
type InitSystem string

const (
	InitSystemd InitSystem = "systemd"
	InitOpenRC  InitSystem = "openrc"
	InitS6      InitSystem = "s6"
	InitRunit   InitSystem = "runit"
	InitSysV    InitSystem = "sysvinit"
	InitUpstart InitSystem = "upstart"
	InitUnknown InitSystem = "unknown"
)

// Environment represents the type of runtime environment detected on the host.
type Environment string

const (
	EnvContainer Environment = "container"
	EnvEmbedded  Environment = "embedded"
	EnvARM       Environment = "arm"
	EnvStandard  Environment = "standard"
)

// LibC represents the C library variant in use on the host.
type LibC string

const (
	LibCGlibc LibC = "glibc"
	LibCMusl  LibC = "musl"
)

// SystemInfo contains all detected properties of the host system needed by
// the installer to select the correct binary variant and service backend.
type SystemInfo struct {
	OS          string
	Arch        string
	LibC        LibC
	InitSystem  InitSystem
	Environment Environment

	// ArchiveSuffix is the arch component of the release tarball name,
	// e.g. "amd64", "arm64", "arm", "riscv64"
	ArchiveSuffix string

	// LibCSuffix is either "" (glibc) or "-musl" (musl), appended after the
	// arch in the release tarball name
	LibCSuffix string
}

// ArchiveName returns the fully-qualified tarball name for the given version,
// e.g. "kubesolo-v1.1.2-linux-amd64.tar.gz"
func (s *SystemInfo) ArchiveName(version string) string {
	return fmt.Sprintf("kubesolo-%s-%s-%s%s.tar.gz", version, s.OS, s.ArchiveSuffix, s.LibCSuffix)
}

// Detect collects all relevant system information and returns a populated
// SystemInfo. It returns an error only for unsupported (untargetable) hosts,
// e.g. a musl system on riscv64 where no musl binary exists.
func Detect() (*SystemInfo, error) {
	arch, archSuffix, err := detectArch()
	if err != nil {
		return nil, err
	}

	libc, libcSuffix, err := detectLibC(archSuffix)
	if err != nil {
		return nil, err
	}

	return &SystemInfo{
		OS:            "linux",
		Arch:          arch,
		ArchiveSuffix: archSuffix,
		LibC:          libc,
		LibCSuffix:    libcSuffix,
		InitSystem:    detectInitSystem(),
		Environment:   detectEnvironment(),
	}, nil
}

// detectArch maps the Go runtime architecture to the KubeSolo release naming
// convention. Returns an error for unsupported architectures.
func detectArch() (goArch, archiveSuffix string, err error) {
	goArch = runtime.GOARCH
	switch goArch {
	case "amd64":
		archiveSuffix = "amd64"
	case "arm64":
		archiveSuffix = "arm64"
	case "arm":
		archiveSuffix = "arm"
	case "riscv64":
		archiveSuffix = "riscv64"
	default:
		return "", "", fmt.Errorf("unsupported architecture: %s", goArch)
	}
	return goArch, archiveSuffix, nil
}

// detectLibC determines whether the host uses glibc or musl by probing the
// presence of musl's dynamic linker under /lib and /usr/lib. The installer
// binary itself is pure Go (CGO_ENABLED=0), so it runs on both — but the
// KubeSolo binary it downloads is CGO-linked and requires the correct variant.
func detectLibC(archSuffix string) (libc LibC, libcSuffix string, err error) {
	muslPatterns := []string{
		"/lib/ld-musl-*.so.1",
		"/usr/lib/ld-musl-*.so.1",
	}
	for _, pattern := range muslPatterns {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			// Musl builds are only published for amd64 and arm64
			if archSuffix != "amd64" && archSuffix != "arm64" {
				return "", "", fmt.Errorf(
					"musl libc detected but musl KubeSolo builds are only available for amd64 and arm64 (current arch: %s). "+
						"See https://docs.kubesolo.io/installation for alternatives",
					archSuffix,
				)
			}
			return LibCMusl, "-musl", nil
		}
	}
	return LibCGlibc, "", nil
}

// detectInitSystem probes the host for a known process supervision system.
// Detection is performed by checking filesystem landmarks and binary presence
// in PATH-equivalent locations — no subprocess execution.
func detectInitSystem() InitSystem {
	// systemd: socket or units directory present and systemctl is reachable
	if fileExists("/run/systemd/private") || (fileExists("/etc/systemd/system") && commandExists("systemctl")) {
		return InitSystemd
	}

	// upstart: sbin/init with --version that mentions "upstart"
	// We check /etc/init as a pragmatic proxy to avoid exec'ing init
	if fileExists("/etc/init") && !fileExists("/etc/systemd") {
		if fileExists("/sbin/initctl") || commandExists("initctl") {
			return InitUpstart
		}
	}

	// OpenRC: openrc binary or /sbin/openrc
	if commandExists("openrc") || fileExists("/sbin/openrc") {
		return InitOpenRC
	}

	// s6: supervision tree directory or s6-svc binary
	if fileExists("/etc/s6") || commandExists("s6-svc") || fileExists("/etc/s6-overlay") {
		return InitS6
	}

	// runit: service directory or runit binary
	if commandExists("runit") || fileExists("/etc/runit") || fileExists("/var/service") {
		return InitRunit
	}

	// SysV: /etc/init.d directory (broad fallback that covers many distros)
	if fileExists("/etc/init.d") {
		return InitSysV
	}

	return InitUnknown
}

// detectEnvironment classifies the runtime context as container, embedded,
// ARM single-board, or standard server/VM.
func detectEnvironment() Environment {
	// Container: Docker or Podman markers
	if fileExists("/.dockerenv") || fileExists("/run/.containerenv") {
		return EnvContainer
	}

	// Check cgroup v1 for docker identity
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(data), "docker") {
			return EnvContainer
		}
	}

	// Embedded single-board computers: Raspberry Pi, BeagleBoard, ODROID etc.
	if data, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		model := strings.ToLower(string(data))
		for _, marker := range []string{"raspberry", "beagle", "odroid", "nano", "rock"} {
			if strings.Contains(model, marker) {
				return EnvEmbedded
			}
		}
	}

	// ARM architecture without a recognised board marker
	if runtime.GOARCH == "arm" || runtime.GOARCH == "arm64" {
		return EnvARM
	}

	return EnvStandard
}

// fileExists reports whether path exists on the filesystem (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// commandExists reports whether a binary named cmd can be found in the standard
// system binary directories. This mirrors `command -v` without exec'ing a shell.
func commandExists(cmd string) bool {
	searchPaths := []string{
		"/usr/local/sbin", "/usr/local/bin",
		"/usr/sbin", "/usr/bin",
		"/sbin", "/bin",
	}
	for _, dir := range searchPaths {
		if _, err := os.Stat(filepath.Join(dir, cmd)); err == nil {
			return true
		}
	}
	return false
}
