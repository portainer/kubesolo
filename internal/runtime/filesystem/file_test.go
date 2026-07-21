package filesystem

import (
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
