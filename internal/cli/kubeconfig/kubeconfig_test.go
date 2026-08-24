package kubeconfig

import "testing"

// ── resolveRealUser ────────────────────────────────────────────────────────────

func TestResolveRealUser_SudoUserWins(t *testing.T) {
	t.Setenv("SUDO_USER", "testuser123")
	t.Setenv("SUDO_UID", "1500")
	t.Setenv("SUDO_GID", "1500")
	t.Setenv("DOAS_USER", "")

	name, home, uid, gid := resolveRealUser()
	if name != "testuser123" {
		t.Errorf("name = %q, want %q", name, "testuser123")
	}
	if home != "/home/testuser123" {
		t.Errorf("home = %q, want %q", home, "/home/testuser123")
	}
	if uid != 1500 || gid != 1500 {
		t.Errorf("uid/gid = %d/%d, want 1500/1500", uid, gid)
	}
}

func TestResolveRealUser_DoasUserWins(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	t.Setenv("DOAS_USER", "testuser456")

	name, home, _, _ := resolveRealUser()
	if name != "testuser456" {
		t.Errorf("name = %q, want %q", name, "testuser456")
	}
	if home != "/home/testuser456" {
		t.Errorf("home = %q, want %q", home, "/home/testuser456")
	}
}

// TestResolveRealUser_FallsBackToRootWithNeitherSet is the case that used to
// be intercepted by the removed /proc/self/loginuid tier — with neither
// SUDO_USER nor DOAS_USER set, attribution must go to whoever the process is
// actually running as ($HOME/$USER), never a guess about who originally
// logged into the session.
func TestResolveRealUser_FallsBackToRootWithNeitherSet(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	t.Setenv("DOAS_USER", "")
	t.Setenv("HOME", "/root")
	t.Setenv("USER", "root")

	name, home, _, _ := resolveRealUser()
	if name != "root" {
		t.Errorf("name = %q, want %q", name, "root")
	}
	if home != "/root" {
		t.Errorf("home = %q, want %q", home, "/root")
	}
}

// TestResolveRealUser_SudoUserRootIsIgnored locks in the existing guard that
// an explicit SUDO_USER=root doesn't short-circuit into a passwd lookup for
// root — it falls through to the same fallback tier as no SUDO_USER at all.
func TestResolveRealUser_SudoUserRootIsIgnored(t *testing.T) {
	t.Setenv("SUDO_USER", "root")
	t.Setenv("DOAS_USER", "")
	t.Setenv("HOME", "/root")
	t.Setenv("USER", "root")

	name, home, _, _ := resolveRealUser()
	if name != "root" {
		t.Errorf("name = %q, want %q", name, "root")
	}
	if home != "/root" {
		t.Errorf("home = %q, want %q", home, "/root")
	}
}
