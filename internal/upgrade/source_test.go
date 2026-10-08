package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/cli/detect"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// writeArchive writes a release-shaped archive holding a kubesolo binary.
func writeArchive(t *testing.T, path string, binary []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"README.md": []byte("readme"), "dist/kubesolo": binary} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
}

func hostInfo() *detect.SystemInfo {
	return &detect.SystemInfo{OS: "linux", Arch: runtime.GOARCH, ArchiveSuffix: runtime.GOARCH, LibC: detect.LibCGlibc}
}

func TestLookupSum(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("B", 64)
	content := []byte(a + "  kubesolo-v1.2.2-linux-amd64.tar.gz\n" + b + " *kubesoloctl-linux-amd64\nbroken line\n")
	if got, ok := lookupSum(content, "kubesolo-v1.2.2-linux-amd64.tar.gz"); !ok || got != a {
		t.Errorf("text-mode entry: %q %v", got, ok)
	}
	if got, ok := lookupSum(content, "kubesoloctl-linux-amd64"); !ok || got != strings.ToLower(b) {
		t.Errorf("binary-mode entry: %q %v", got, ok)
	}
	if _, ok := lookupSum(content, "kubesolo-v1.2.2-linux-arm64.tar.gz"); ok {
		t.Error("found a missing entry")
	}
}

// Every way of failing before the binary is even looked at must leave nothing
// but the run's own staging directory touched.
func TestStageRejectsBeforeTouchingAnything(t *testing.T) {
	l := testLayout(t)
	src := filepath.Join(t.TempDir(), "kubesolo-v1.2.2-linux-amd64.tar.gz")
	writeArchive(t, src, []byte("not really a binary"))
	raw, _ := os.ReadFile(src)
	staging := l.RunStagingDir("t")
	logf := func(string, ...any) {}

	// No checksum anywhere.
	_, err := Stage(context.Background(), l, staging, Request{Version: "v1.2.2", Source: src}, hostInfo(), logf)
	if err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Errorf("no checksum: %v", err)
	}

	// Wrong checksum in the request.
	_, err = Stage(context.Background(), l, staging, Request{Version: "v1.2.2", Source: src, SHA256: strings.Repeat("0", 64)}, hostInfo(), logf)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("wrong checksum: %v", err)
	}

	// Wrong checksum in SHA256SUMS beside it.
	sums := filepath.Join(filepath.Dir(src), SumsFile)
	if err := os.WriteFile(sums, []byte(strings.Repeat("1", 64)+"  "+filepath.Base(src)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Stage(context.Background(), l, staging, Request{Version: "v1.2.2", Source: src}, hostInfo(), logf)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || !strings.Contains(err.Error(), SumsFile) {
		t.Errorf("wrong SHA256SUMS: %v", err)
	}

	// Right checksum, but the archive holds no executable.
	if err := os.WriteFile(sums, []byte(sum(raw)+"  "+filepath.Base(src)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Stage(context.Background(), l, staging, Request{Version: "v1.2.2", Source: src}, hostInfo(), logf)
	if err == nil || !strings.Contains(err.Error(), "not a Linux executable") {
		t.Errorf("non-ELF binary: %v", err)
	}

	// A corrupt archive with a matching checksum.
	bad := filepath.Join(t.TempDir(), "kubesolo-v1.2.2-linux-amd64.tar.gz")
	if err := os.WriteFile(bad, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Stage(context.Background(), l, staging, Request{Version: "v1.2.2", Source: bad, SHA256: sum([]byte("garbage"))}, hostInfo(), logf)
	if err == nil || !strings.Contains(err.Error(), "not a gzip archive") {
		t.Errorf("corrupt archive: %v", err)
	}

	if _, err := os.Stat(l.Binary); !os.IsNotExist(err) {
		t.Error("the installed binary path was touched")
	}
}

func TestStageFromMirror(t *testing.T) {
	l := testLayout(t)
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	writeArchive(t, archive, []byte("binary"))
	raw, _ := os.ReadFile(archive)
	name := hostInfo().ArchiveName("v1.2.2")

	var withSums bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.2.2/" + name:
			_, _ = w.Write(raw)
		case "/v1.2.2/" + SumsFile:
			if !withSums {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprintf(w, "%s  %s\n", sum(raw), name)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("KUBESOLO_RELEASE_BASE_URL", srv.URL+"/")

	// A mirror with no SHA256SUMS: refused, not trusted.
	_, err := Stage(context.Background(), l, l.RunStagingDir("m"), Request{Version: "v1.2.2"}, hostInfo(), func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "must publish") {
		t.Errorf("mirror without sums: %v", err)
	}

	// With SHA256SUMS the download verifies and gets as far as the binary.
	withSums = true
	_, err = Stage(context.Background(), l, l.RunStagingDir("m"), Request{Version: "v1.2.2"}, hostInfo(), func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "not a Linux executable") {
		t.Errorf("verified download, fake binary: %v", err)
	}

	// A version the mirror does not have.
	_, err = Stage(context.Background(), l, l.RunStagingDir("m"), Request{Version: "v9.9.9"}, hostInfo(), func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing version: %v", err)
	}
}

func TestRequireFree(t *testing.T) {
	if err := RequireFree(t.TempDir(), 1); err != nil {
		t.Errorf("1 byte: %v", err)
	}
	if err := RequireFree(t.TempDir(), 1<<62); err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Errorf("4 EiB: %v", err)
	}
}

// The test binary itself is an ELF executable for this host on Linux.
func TestCheckBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a Linux executable")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckBinary(self, hostInfo()); err != nil {
		t.Errorf("own binary: %v", err)
	}

	other := hostInfo()
	other.Arch = map[string]string{"amd64": "arm64"}[runtime.GOARCH]
	if other.Arch == "" {
		other.Arch = "amd64"
	}
	if err := CheckBinary(self, other); err == nil || !strings.Contains(err.Error(), "built for") {
		t.Errorf("other architecture: %v", err)
	}
}
