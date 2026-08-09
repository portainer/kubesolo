package filesystem

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureSymbolicLink(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("absent destination on non-writable directory", func(t *testing.T) {
		dir := nonWritableDir(t)
		target := filepath.Join(dir, "link")
		if err := EnsureSymbolicLink(source, target); err == nil {
			t.Fatal("expected installation on a non-writable directory to fail")
		}
	})

	t.Run("existing correct symlink on non-writable directory", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "link")
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
		makeNonWritable(t, dir)
		if err := EnsureSymbolicLink(source, target); err != nil {
			t.Fatalf("correct symlink should be preserved: %v", err)
		}
	})

	t.Run("existing incorrect symlink on non-writable directory", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "link")
		if err := os.Symlink(filepath.Join(dir, "wrong"), target); err != nil {
			t.Fatal(err)
		}
		makeNonWritable(t, dir)
		if err := EnsureSymbolicLink(source, target); err == nil {
			t.Fatal("expected replacement on a non-writable directory to fail")
		}
	})

	t.Run("absent destination on writable directory", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "link")
		if err := EnsureSymbolicLink(source, target); err != nil {
			t.Fatal(err)
		}
		assertLink(t, target, source)
	})

	t.Run("existing incorrect symlink on writable directory", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink("wrong", target); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSymbolicLink(source, target); err != nil {
			t.Fatal(err)
		}
		assertLink(t, target, source)
	})
}

func nonWritableDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	makeNonWritable(t, dir)
	return dir
}

func makeNonWritable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not enforce writability on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode bits")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func TestRemoveIfSymlinkRemovesLinkOnly(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}

	if !RemoveIfSymlink(link) {
		t.Fatal("RemoveIfSymlink(symlink) = false, want true")
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("symlink was not removed")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("the symlink's target must survive: %v", err)
	}
}

func TestRemoveIfSymlinkPreservesRealFiles(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if RemoveIfSymlink(file) {
		t.Fatal("RemoveIfSymlink(regular file) = true, want false")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("regular file must survive: %v", err)
	}

	if RemoveIfSymlink(filepath.Join(dir, "missing")) {
		t.Fatal("RemoveIfSymlink(missing) = true, want false")
	}
}

// TestRemoveIfSymlinkPreservesSocket is the regression this function exists for: a
// container runtime managed by the host binds a real socket where kubesolo would
// otherwise install its symlink, and deleting it cuts every client on the machine
// off from that runtime.
func TestRemoveIfSymlinkPreservesSocket(t *testing.T) {
	// Unix socket paths are limited to ~104 bytes, so use a short directory rather
	// than t.TempDir(), which can be long enough to exceed it.
	dir, err := os.MkdirTemp("/tmp", "ks")
	if err != nil {
		t.Skipf("cannot create a short temp dir for a unix socket: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "s.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("cannot create a unix socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	if RemoveIfSymlink(socket) {
		t.Fatal("RemoveIfSymlink(socket) = true, want false")
	}
	if _, err := os.Lstat(socket); err != nil {
		t.Fatalf("a real socket must survive: %v", err)
	}
}

func assertLink(t *testing.T, target, source string) {
	t.Helper()
	destination, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if destination != source {
		t.Fatalf("link destination = %q, want %q", destination, source)
	}
}
