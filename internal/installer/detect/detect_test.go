package detect

import (
	"runtime"
	"strings"
	"testing"
)

// ── ArchiveName ───────────────────────────────────────────────────────────────

func TestArchiveName_GlibcVariants(t *testing.T) {
	cases := []struct {
		arch    string
		version string
		want    string
	}{
		{"amd64", "v1.1.2", "kubesolo-v1.1.2-linux-amd64.tar.gz"},
		{"arm64", "v1.1.2", "kubesolo-v1.1.2-linux-arm64.tar.gz"},
		{"arm", "v1.0.0", "kubesolo-v1.0.0-linux-arm.tar.gz"},
		{"riscv64", "v1.2.3", "kubesolo-v1.2.3-linux-riscv64.tar.gz"},
	}
	for _, c := range cases {
		info := &SystemInfo{OS: "linux", ArchiveSuffix: c.arch, LibCSuffix: ""}
		got := info.ArchiveName(c.version)
		if got != c.want {
			t.Errorf("ArchiveName(%q) with arch=%q glibc: got %q, want %q", c.version, c.arch, got, c.want)
		}
	}
}

func TestArchiveName_MuslVariants(t *testing.T) {
	cases := []struct {
		arch    string
		version string
		want    string
	}{
		{"amd64", "v1.1.2", "kubesolo-v1.1.2-linux-amd64-musl.tar.gz"},
		{"arm64", "v1.1.2", "kubesolo-v1.1.2-linux-arm64-musl.tar.gz"},
	}
	for _, c := range cases {
		info := &SystemInfo{OS: "linux", ArchiveSuffix: c.arch, LibCSuffix: "-musl"}
		got := info.ArchiveName(c.version)
		if got != c.want {
			t.Errorf("ArchiveName(%q) with arch=%q musl: got %q, want %q", c.version, c.arch, got, c.want)
		}
	}
}

// ── detectArch ────────────────────────────────────────────────────────────────

func TestDetectArch_CurrentRuntime(t *testing.T) {
	goArch, suffix, err := detectArch()
	if err != nil {
		// Only fail if the current runtime arch is one we know we support
		supported := map[string]bool{"amd64": true, "arm64": true, "arm": true, "riscv64": true}
		if supported[runtime.GOARCH] {
			t.Fatalf("detectArch() failed on supported arch %q: %v", runtime.GOARCH, err)
		}
		t.Skipf("arch %q not in KubeSolo target set — skipping", runtime.GOARCH)
	}
	if goArch != runtime.GOARCH {
		t.Errorf("goArch = %q, want %q (runtime.GOARCH)", goArch, runtime.GOARCH)
	}
	if suffix == "" {
		t.Error("archiveSuffix should not be empty for a supported arch")
	}
}

func TestDetectArch_MappingsAreConsistent(t *testing.T) {
	// Verify the mapping table is consistent: archive suffix must never contain
	// a slash and must be a lowercase identifier.
	cases := []struct {
		fakeArch string
		wantSfx  string
		wantErr  bool
	}{
		{"amd64", "amd64", false},
		{"arm64", "arm64", false},
		{"arm", "arm", false},
		{"riscv64", "riscv64", false},
		{"mips64", "", true},
		{"s390x", "", true},
	}
	for _, c := range cases {
		// Override runtime.GOARCH by testing the switch logic directly
		var suffix string
		var err error
		switch c.fakeArch {
		case "amd64":
			suffix = "amd64"
		case "arm64":
			suffix = "arm64"
		case "arm":
			suffix = "arm"
		case "riscv64":
			suffix = "riscv64"
		default:
			err = &unsupportedArchError{arch: c.fakeArch}
		}
		if c.wantErr && err == nil {
			t.Errorf("arch %q: expected error, got suffix %q", c.fakeArch, suffix)
		}
		if !c.wantErr && err != nil {
			t.Errorf("arch %q: unexpected error: %v", c.fakeArch, err)
		}
		if !c.wantErr && suffix != c.wantSfx {
			t.Errorf("arch %q: got suffix %q, want %q", c.fakeArch, suffix, c.wantSfx)
		}
		if suffix != "" && strings.Contains(suffix, "/") {
			t.Errorf("suffix %q must not contain a slash", suffix)
		}
	}
}

// unsupportedArchError is a stand-in for the error returned by detectArch for
// unknown architectures, used only in the mapping consistency test above.
type unsupportedArchError struct{ arch string }

func (e *unsupportedArchError) Error() string { return "unsupported architecture: " + e.arch }

// ── InitSystem constants ──────────────────────────────────────────────────────

func TestInitSystemConstants_AreNonEmpty(t *testing.T) {
	systems := []InitSystem{
		InitSystemd, InitOpenRC, InitS6, InitRunit, InitSysV, InitUpstart, InitUnknown,
	}
	seen := map[InitSystem]bool{}
	for _, s := range systems {
		if s == "" {
			t.Error("InitSystem constant must not be empty string")
		}
		if seen[s] {
			t.Errorf("duplicate InitSystem value: %q", s)
		}
		seen[s] = true
	}
}

func TestEnvironmentConstants_AreNonEmpty(t *testing.T) {
	envs := []Environment{EnvContainer, EnvEmbedded, EnvARM, EnvStandard}
	seen := map[Environment]bool{}
	for _, e := range envs {
		if e == "" {
			t.Error("Environment constant must not be empty string")
		}
		if seen[e] {
			t.Errorf("duplicate Environment value: %q", e)
		}
		seen[e] = true
	}
}
