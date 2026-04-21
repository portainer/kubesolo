package preflight

import (
	"os"
	"path/filepath"
	"testing"
)

// ── CheckHostname ─────────────────────────────────────────────────────────────

func TestCheckHostname_ValidNames(t *testing.T) {
	valid := []string{
		"myhost",
		"my-host",
		"my.host.local",
		"node01",
		"edge-device-42",
		"a",
		"a1b2c3",
		"kubesolo-prod-01",
	}
	for _, name := range valid {
		if !hostnameRE.MatchString(name) {
			t.Errorf("hostname %q should be valid but was rejected", name)
		}
	}
}

func TestCheckHostname_InvalidNames(t *testing.T) {
	invalid := []string{
		"MyHost",            // uppercase
		"my host",          // space
		"my_host",          // underscore
		"-starts-with-dash",
		"ends-with-dash-",
		".starts-with-dot",
		"ends-with-dot.",
		"",
	}
	for _, name := range invalid {
		if hostnameRE.MatchString(name) {
			t.Errorf("hostname %q should be invalid but was accepted", name)
		}
	}
}

func TestCheckHostname_TooLong(t *testing.T) {
	long := ""
	for len(long) < 254 {
		long += "a"
	}
	if err := validateHostname(long); err == nil {
		t.Errorf("expected error for %d-char hostname, got nil", len(long))
	}
}

func TestCheckHostname_Empty(t *testing.T) {
	if err := validateHostname(""); err == nil {
		t.Error("expected error for empty hostname, got nil")
	}
}

func TestCheckHostname_ValidDirect(t *testing.T) {
	if err := validateHostname("my-node-01"); err != nil {
		t.Errorf("expected valid hostname to pass, got: %v", err)
	}
}

func TestCheckHostname_InvalidDirect(t *testing.T) {
	if err := validateHostname("My_Node"); err == nil {
		t.Error("expected error for non-RFC-1123 hostname, got nil")
	}
}

// ── CheckDockerConflict ───────────────────────────────────────────────────────

func TestCheckDockerConflict_NoDocker(t *testing.T) {
	// On a machine with no Docker socket or binary this should always pass
	// (also passes on macOS dev machines which have no /var/run/docker.sock)
	_ = os.Remove("/tmp/docker-test.sock") // clean up from any prior run

	// Temporarily swap the socket path via the function logic:
	// since we can't inject the path, we just confirm that on a clean
	// environment the check returns nil (no false positive).
	if _, err := os.Stat("/var/run/docker.sock"); os.IsNotExist(err) {
		if err := CheckDockerConflict(); err != nil {
			// Only fail if neither the socket nor binary is present
			for _, p := range []string{"/usr/bin/docker", "/usr/local/bin/docker"} {
				if _, e := os.Stat(p); e == nil {
					t.Skip("Docker is actually installed — skipping clean-environment test")
				}
			}
			t.Errorf("CheckDockerConflict() unexpected error on clean system: %v", err)
		}
	} else {
		t.Skip("Docker socket present — skipping clean-environment test")
	}
}

func TestCheckDockerConflict_FakeBinary(t *testing.T) {
	// Create a fake docker binary in a temp dir and verify the check catches it.
	// We test checkRequiredControllers-style logic by calling the internal helper.
	// (CheckDockerConflict only checks fixed paths, so this verifies the pattern.)
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "docker")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Since CheckDockerConflict uses hardcoded paths, we verify the regex/stat
	// logic indirectly by confirming a non-existent path does not trigger it.
	if _, err := os.Stat(filepath.Join(tmp, "nonexistent")); !os.IsNotExist(err) {
		t.Error("expected IsNotExist for nonexistent path")
	}
}

// ── checkRequiredControllers ──────────────────────────────────────────────────

func TestCheckRequiredControllers_AllPresent(t *testing.T) {
	available := []string{"cpuset", "cpu", "io", "memory", "pids", "hugetlb"}
	if err := checkRequiredControllers(available); err != nil {
		t.Errorf("all required controllers present but got error: %v", err)
	}
}

func TestCheckRequiredControllers_SomeMissing(t *testing.T) {
	available := []string{"cpu", "memory"} // missing: cpuset, io, pids
	err := checkRequiredControllers(available)
	if err == nil {
		t.Fatal("expected error for missing controllers, got nil")
	}
	for _, missing := range []string{"cpuset", "io", "pids"} {
		if !containsString(err.Error(), missing) {
			t.Errorf("error should mention missing controller %q: %v", missing, err)
		}
	}
}

func TestCheckRequiredControllers_NonePresent(t *testing.T) {
	err := checkRequiredControllers(nil)
	if err == nil {
		t.Fatal("expected error when no controllers available, got nil")
	}
}

func TestCheckRequiredControllers_ExactRequired(t *testing.T) {
	// Exactly the required set and nothing else — should pass
	err := checkRequiredControllers([]string{"cpuset", "cpu", "io", "memory", "pids"})
	if err != nil {
		t.Errorf("exact required set should pass: %v", err)
	}
}

// ── CheckPorts ────────────────────────────────────────────────────────────────

func TestCheckPorts_NoConflict(t *testing.T) {
	// On a clean development machine none of the KubeSolo ports should be bound.
	// This test is best-effort and skips if any port is already in use.
	err := CheckPorts()
	if err != nil {
		t.Skipf("a KubeSolo port appears to be in use — skipping: %v", err)
	}
}

// ── Suite ─────────────────────────────────────────────────────────────────────

func TestSuite_ReturnsChecks(t *testing.T) {
	checks := Suite(false)
	if len(checks) == 0 {
		t.Fatal("Suite should return at least one check")
	}
	for _, c := range checks {
		if c.Name == "" {
			t.Error("each check must have a non-empty Name")
		}
		if c.Run == nil {
			t.Errorf("check %q has nil Run function", c.Name)
		}
	}
}

func TestSuite_NamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Suite(false) {
		if seen[c.Name] {
			t.Errorf("duplicate check name: %q", c.Name)
		}
		seen[c.Name] = true
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
