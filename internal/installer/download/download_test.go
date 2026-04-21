package download

import (
	"archive/tar"
	"compress/gzip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// ── extractTarGz ─────────────────────────────────────────────────────────────

func TestExtractTarGz_ExtractsBinary(t *testing.T) {
	content := []byte("#!/bin/sh\necho kubesolo\n")
	archive := buildTarGz(t, "kubesolo", content)

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "kubesolo.tar.gz")
	if err := os.WriteFile(archivePath, archive, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(archivePath, tmpDir, "kubesolo"); err != nil {
		t.Fatalf("extractTarGz failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(tmpDir, "kubesolo"))
	if err != nil {
		t.Fatalf("extracted file not found: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extracted content mismatch: got %q, want %q", got, content)
	}
}

func TestExtractTarGz_NestedPath(t *testing.T) {
	// Tarball with a directory prefix like "kubesolo-v1.1.2-linux-amd64/kubesolo"
	content := []byte("binary content")
	archive := buildTarGzWithPrefix(t, "release-dir/kubesolo", content)

	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	if err := os.WriteFile(archivePath, archive, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(archivePath, tmpDir, "kubesolo"); err != nil {
		t.Fatalf("extractTarGz with nested path failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "kubesolo")); err != nil {
		t.Error("binary not found after extracting from nested path")
	}
}

func TestExtractTarGz_MissingTarget(t *testing.T) {
	archive := buildTarGz(t, "something-else", []byte("data"))
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	_ = os.WriteFile(archivePath, archive, 0o644)

	err := extractTarGz(archivePath, tmpDir, "kubesolo")
	if err == nil {
		t.Fatal("expected error when target binary not in archive, got nil")
	}
}

func TestExtractTarGz_CorruptArchive(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "corrupt.tar.gz")
	_ = os.WriteFile(archivePath, []byte("this is not a valid gzip stream"), 0o644)

	err := extractTarGz(archivePath, tmpDir, "kubesolo")
	if err == nil {
		t.Fatal("expected error for corrupt archive, got nil")
	}
}

// ── copyFile ──────────────────────────────────────────────────────────────────

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.txt")
	dst := filepath.Join(tmpDir, "dst.txt")

	content := []byte("hello kubesolo")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("dst file not readable: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("copy content mismatch: got %q, want %q", got, content)
	}
}

func TestCopyFile_MissingSrc(t *testing.T) {
	tmpDir := t.TempDir()
	err := copyFile(filepath.Join(tmpDir, "nonexistent"), filepath.Join(tmpDir, "dst"))
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

// ── progressReader ────────────────────────────────────────────────────────────

func TestProgressReader_ReadsAllBytes(t *testing.T) {
	data := []byte("hello world - some download content")
	r := &progressReader{r: bytes.NewReader(data), total: int64(len(data))}

	buf := make([]byte, len(data))
	n, err := r.Read(buf)
	if err != nil && err.Error() != "EOF" {
		t.Fatalf("Read error: %v", err)
	}
	if n != len(data) {
		t.Errorf("read %d bytes, want %d", n, len(data))
	}
	if !bytes.Equal(buf[:n], data) {
		t.Errorf("read content mismatch")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// buildTarGz creates an in-memory tar.gz with a single file at the top level.
func buildTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	return buildTarGzWithPrefix(t, name, content)
}

// buildTarGzWithPrefix creates an in-memory tar.gz with a file at an arbitrary path.
func buildTarGzWithPrefix(t *testing.T, path string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: path,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar WriteHeader: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}
