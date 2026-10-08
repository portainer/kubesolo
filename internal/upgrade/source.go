package upgrade

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/portainer/kubesolo/internal/cli/detect"
)

// DefaultReleaseBaseURL is where releases are downloaded from.
const DefaultReleaseBaseURL = "https://github.com/portainer/kubesolo/releases/download"

// githubReleaseAPI is consulted for an asset's digest when a release has no
// SHA256SUMS file. GitHub computes a SHA-256 digest for every release asset;
// releases up to v1.2.1 publish nothing else.
const githubReleaseAPI = "https://api.github.com/repos/portainer/kubesolo/releases/tags/"

// SumsFile is the checksum file published with each release, in sha256sum
// format, and expected next to an archive staged by hand.
const SumsFile = "SHA256SUMS"

// ReleaseBaseURL is where releases are downloaded from: DefaultReleaseBaseURL,
// or a mirror named by KUBESOLO_RELEASE_BASE_URL. A mirror must publish
// SHA256SUMS with each release; only GitHub can stand in for it.
func ReleaseBaseURL() string {
	if v := strings.TrimRight(os.Getenv("KUBESOLO_RELEASE_BASE_URL"), "/"); v != "" {
		return v
	}
	return DefaultReleaseBaseURL
}

// ErrNoChecksum is returned for a local source with no checksum to check it
// against.
var ErrNoChecksum = errors.New("no checksum")

// Staged is a release that has been fetched and verified, ready to install.
type Staged struct {
	Version string

	// Binary is the extracted kubesolo binary in the staging directory.
	Binary string

	// SHA256 is the checksum of what was fetched (the archive, or the binary
	// when the source was a bare binary), and ChecksumSource where the expected
	// value came from.
	SHA256         string
	ChecksumSource string
}

// Logf reports progress.
type Logf func(format string, args ...any)

// Stage fetches the release the request names, verifies it, and extracts the
// binary into staging, a directory of the caller's own under l.StagingDir().
// Nothing outside it is touched, so any error leaves the host as it was.
func Stage(ctx context.Context, l Layout, staging string, req Request, info *detect.SystemInfo, logf Logf) (*Staged, error) {
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, err
	}

	archiveName := info.ArchiveName(req.Version)
	staged := &Staged{Version: req.Version, Binary: filepath.Join(staging, "kubesolo")}

	// The binary is about twice the size of the archive. Checking before the
	// download avoids filling the disk with half an archive.
	if err := RequireFree(staging, estimateStagingBytes(l)); err != nil {
		return nil, err
	}

	var fetched string // the file the checksum is over
	switch req.Source {
	case "":
		url := fmt.Sprintf("%s/%s/%s", ReleaseBaseURL(), req.Version, archiveName)
		fetched = filepath.Join(staging, archiveName)
		logf("downloading %s", url)
		if err := download(ctx, url, fetched); err != nil {
			return nil, err
		}
	default:
		fi, err := os.Stat(req.Source)
		if err != nil {
			return nil, fmt.Errorf("source: %w", err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("source %s is not a regular file", req.Source)
		}
		fetched = req.Source
		// A source anywhere else may be writable by someone other than root,
		// who could replace it between the checksum below and its extraction,
		// or change a bare binary through the hard link staging makes of it,
		// and have different code run as root than the code that was verified.
		// So it is copied into staging, which only root can write, and every
		// check and use is of the copy. A source already in staging was put
		// there by kubesoloctl or install.sh, as root.
		if !isWithin(req.Source, l.StagingDir()) {
			// Its own name: a bare binary is usually called kubesolo, which
			// is where the staged binary goes.
			fetched = filepath.Join(staging, "source-"+filepath.Base(req.Source))
			if err := copyFile(req.Source, fetched, 0o600); err != nil {
				return nil, fmt.Errorf("copy %s into staging: %w", req.Source, err)
			}
		}
		logf("using %s", req.Source)
	}

	want, from, err := expectedChecksum(ctx, req, archiveName)
	if err != nil {
		return nil, err
	}
	got, err := fileSHA256(fetched)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(got, want) {
		name := archiveName
		if req.Source != "" {
			name = filepath.Base(req.Source)
		}
		return nil, fmt.Errorf("checksum mismatch for %s: expected %s (%s), got %s", name, want, from, got)
	}
	staged.SHA256, staged.ChecksumSource = got, from
	logf("checksum verified against %s", from)

	if isArchive(fetched) {
		if err := extractBinary(fetched, staged.Binary); err != nil {
			return nil, err
		}
	} else if err := linkOrCopy(fetched, staged.Binary); err != nil {
		return nil, err
	}

	if err := CheckBinary(staged.Binary, info); err != nil {
		return nil, err
	}

	reported, err := BinaryVersion(ctx, staged.Binary)
	if err != nil {
		return nil, err
	}
	if reported != req.Version {
		return nil, fmt.Errorf("the binary reports itself as %s, not %s", reported, req.Version)
	}
	logf("staged kubesolo %s (%s, %s)", reported, info.Arch, libcName(info))
	return staged, nil
}

func libcName(info *detect.SystemInfo) string {
	if info.LibC == "" {
		return "glibc"
	}
	return string(info.LibC)
}

func isArchive(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

// estimateStagingBytes is the space staging needs: the archive and the binary
// extracted from it, each about the size of the installed binary, plus margin.
func estimateStagingBytes(l Layout) uint64 {
	size := uint64(250 << 20)
	if fi, err := os.Stat(l.Binary); err == nil {
		size = uint64(fi.Size())
	}
	return 2*size + 64<<20
}

// RequireFree returns an error unless the filesystem holding path has at least
// need bytes available.
func RequireFree(path string, need uint64) error {
	free, err := FreeBytes(path)
	if err != nil {
		return fmt.Errorf("check free space on %s: %w", path, err)
	}
	if free < need {
		return fmt.Errorf("not enough free space on %s: %d MiB available, %d MiB needed", path, free>>20, need>>20)
	}
	return nil
}

// FreeBytes is the space available to an unprivileged user on the filesystem
// holding path. Root can use the reserved blocks too, but an upgrade that
// relies on them leaves the host with a full disk.
func FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// expectedChecksum finds the checksum fetched should have, and says where it
// came from.
func expectedChecksum(ctx context.Context, req Request, archiveName string) (sum, source string, err error) {
	if req.SHA256 != "" {
		return strings.ToLower(req.SHA256), "the request", nil
	}

	if req.Source != "" {
		// The name the checksum file knows it by is the original's, not the
		// staged copy's.
		name := filepath.Base(req.Source)
		sumsPath := filepath.Join(filepath.Dir(req.Source), SumsFile)
		raw, err := os.ReadFile(sumsPath)
		if err != nil {
			return "", "", fmt.Errorf("%w for %s: pass sha256 with the request, or put %s next to it (%v)", ErrNoChecksum, req.Source, SumsFile, err)
		}
		if sum, ok := lookupSum(raw, name); ok {
			return sum, sumsPath, nil
		}
		return "", "", fmt.Errorf("%s has no entry for %s", sumsPath, name)
	}

	return ReleaseChecksum(ctx, req.Version, archiveName)
}

// ReleaseChecksum finds the published checksum of a release asset: from the
// release's SHA256SUMS, or, for a GitHub release without one, from the digest
// GitHub computed for the asset.
func ReleaseChecksum(ctx context.Context, version, name string) (sum, source string, err error) {
	archiveName := name
	base := ReleaseBaseURL()
	sumsURL := fmt.Sprintf("%s/%s/%s", base, version, SumsFile)
	raw, status, err := httpGet(ctx, sumsURL, 1<<20)
	switch {
	case err == nil && status == http.StatusOK:
		if sum, ok := lookupSum(raw, archiveName); ok {
			return sum, sumsURL, nil
		}
		return "", "", fmt.Errorf("%s has no entry for %s", sumsURL, archiveName)
	case err == nil && status == http.StatusNotFound && base == DefaultReleaseBaseURL:
		return githubAssetDigest(ctx, version, archiveName)
	case err == nil:
		return "", "", fmt.Errorf("GET %s returned HTTP %d; a release mirror must publish %s", sumsURL, status, SumsFile)
	default:
		return "", "", err
	}
}

// lookupSum finds name in sha256sum-format content.
func lookupSum(content []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// githubAssetDigest asks GitHub for the digest of a release asset.
func githubAssetDigest(ctx context.Context, version, name string) (string, string, error) {
	url := githubReleaseAPI + version
	raw, status, err := httpGet(ctx, url, 4<<20)
	if err != nil {
		return "", "", err
	}
	if status != http.StatusOK {
		return "", "", fmt.Errorf("release %s has no %s, and GET %s returned HTTP %d", version, SumsFile, url, status)
	}
	var release struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &release); err != nil {
		return "", "", fmt.Errorf("could not read %s: %w", url, err)
	}
	for _, a := range release.Assets {
		if a.Name == name {
			sum, ok := strings.CutPrefix(a.Digest, "sha256:")
			if !ok || len(sum) != 64 {
				return "", "", fmt.Errorf("GitHub reports no SHA-256 digest for %s", name)
			}
			return strings.ToLower(sum), "the GitHub release asset digest", nil
		}
	}
	return "", "", fmt.Errorf("release %s has no asset %s", version, name)
}

const fetchTimeout = 15 * time.Minute

func httpGet(ctx context.Context, url string, limit int64) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %w", url, err)
	}
	return raw, resp.StatusCode, nil
}

func download(ctx context.Context, url, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", url, resp.StatusCode)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		return fmt.Errorf("GET %s: %w", url, err)
	}
	return out.Close()
}

// FileSHA256 returns the hex SHA-256 of a file.
func FileSHA256(path string) (string, error) { return fileSHA256(path) }

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary writes the kubesolo binary from a release archive to dest.
func extractBinary(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not a gzip archive: %w", archive, err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s does not contain a kubesolo binary", archive)
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", archive, err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "kubesolo" {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return fmt.Errorf("extract kubesolo from %s: %w", archive, err)
		}
		return out.Close()
	}
}

// CheckBinary confirms path is an executable for this host: the right machine
// architecture and, for a dynamically linked binary, the right C library.
// Running a binary for another architecture fails with "exec format error";
// one for the other libc fails with "not found", which is far less obvious.
func CheckBinary(path string, info *detect.SystemInfo) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s is not a Linux executable: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()

	want, ok := map[string]elf.Machine{
		"amd64":   elf.EM_X86_64,
		"arm64":   elf.EM_AARCH64,
		"arm":     elf.EM_ARM,
		"riscv64": elf.EM_RISCV,
	}[info.Arch]
	if !ok {
		return fmt.Errorf("unsupported architecture %q", info.Arch)
	}
	if f.Machine != want {
		return fmt.Errorf("the binary is built for %s, but this host is %s", f.Machine, info.Arch)
	}

	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		raw, err := io.ReadAll(p.Open())
		if err != nil {
			return fmt.Errorf("read the binary's interpreter: %w", err)
		}
		interp := string(bytes.TrimRight(raw, "\x00"))
		isMusl := strings.Contains(interp, "musl")
		switch {
		case info.LibC == detect.LibCMusl && !isMusl:
			return fmt.Errorf("the binary is linked against glibc (%s), but this host uses musl", interp)
		case info.LibC != detect.LibCMusl && isMusl:
			return fmt.Errorf("the binary is linked against musl (%s), but this host uses glibc", interp)
		}
	}
	return nil
}

// BinaryVersion runs `<path> --version` and returns the version it reports.
func BinaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s --version failed: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	v, ok := ReportedVersion(string(out))
	if !ok {
		return "", fmt.Errorf("%s --version printed no version: %s", path, strings.TrimSpace(string(out)))
	}
	return v, nil
}

// Install puts a staged binary in place at dst atomically: a copy beside dst,
// then a rename over it. A running process keeps the old inode.
func Install(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".upgrade"
	if err := copyFile(src, tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// VersionFromArchiveName returns the version in a release archive's name, such
// as v1.2.2 in kubesolo-v1.2.2-linux-arm64-musl.tar.gz or
// kubesolo-v1.2.2-linux-amd64-offline.tar.gz.
func VersionFromArchiveName(path string) (string, bool) {
	m := archiveNamePattern.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// The version is matched lazily, so the -linux-<arch>[-musl][-offline] suffix
// is never taken for part of a pre-release.
var archiveNamePattern = regexp.MustCompile(`^kubesolo-(v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+?)?)-linux-[a-z0-9]+(?:-musl)?(?:-offline)?\.(?:tar\.gz|tgz)$`)

// isWithin reports whether path is inside dir, after resolving symlinks in both,
// so a link into staging from elsewhere does not count.
func isWithin(path, dir string) bool {
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(d, p)
	return err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

// linkOrCopy hard-links src to dst, so staging a binary already on the same
// filesystem costs no space, and copies it otherwise. The checksum was verified
// on src, and a hard link is the same inode, so it still holds for dst.
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return os.Chmod(dst, 0o755)
	}
	return copyFile(src, dst, 0o755)
}

// copyFile copies src to dst with mode, syncing dst.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
