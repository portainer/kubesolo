// Package download handles fetching, verifying, and extracting the KubeSolo
// binary from GitHub Releases, as well as offline installation from a local
// archive or binary file.
package download

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/rs/zerolog/log"
)

const (
	releaseBaseURL  = "https://github.com/portainer/kubesolo/releases/download"
	downloadTimeout = 10 * time.Minute
)

// Install places the KubeSolo binary at /usr/local/bin/kubesolo.
// If offlineSrc is non-empty it is used as the source instead of downloading.
// Supported formats: .tar.gz / .tgz (archive containing the kubesolo binary),
// or any other path (treated as a raw binary and copied directly).
// Otherwise the binary is fetched from GitHub Releases using archiveName and
// version to build the URL.
func Install(offlineSrc, archiveName, version string) error {
	if offlineSrc != "" {
		return installOffline(offlineSrc)
	}
	return installOnline(archiveName, version)
}

// DownloadBundle downloads the KubeSolo release tarball into outDir and copies
// the running kubesoloctl binary alongside it, producing a fully self-contained
// offline bundle ready to be transferred to an air-gapped machine.
//
//   - archiveName is the kubesolo release tarball, e.g. "kubesolo-v1.1.7-linux-amd64.tar.gz"
//   - version     is the kubesolo release tag, e.g. "v1.1.7"
func DownloadBundle(outDir, archiveName, version string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	// 1. KubeSolo binary tarball
	tarURL := fmt.Sprintf("%s/%s/%s", releaseBaseURL, version, archiveName)
	tarDest := filepath.Join(outDir, archiveName)
	log.Info().Msgf("downloading KubeSolo %s (%s)...", version, archiveName)
	if err := downloadFile(tarURL, tarDest); err != nil {
		return fmt.Errorf("failed to download KubeSolo archive: %w", err)
	}
	log.Info().Msgf("KubeSolo archive saved to: %s", tarDest)

	// 2. Copy the running kubesoloctl binary — no need to download what we already have.
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate running kubesoloctl binary: %w", err)
	}
	installerDest := filepath.Join(outDir, "kubesoloctl")
	log.Info().Msgf("copying kubesoloctl to %s...", installerDest)
	if err := copyFile(selfPath, installerDest); err != nil {
		return fmt.Errorf("failed to copy kubesoloctl binary: %w", err)
	}
	if err := os.Chmod(installerDest, 0o755); err != nil {
		return fmt.Errorf("failed to make kubesoloctl executable: %w", err)
	}
	log.Info().Msgf("kubesoloctl saved to: %s", installerDest)
	return nil
}

// ── online installation ────────────────────────────────────────────────────────

func installOnline(archiveName, version string) error {
	url := fmt.Sprintf("%s/%s/%s", releaseBaseURL, version, archiveName)
	log.Info().Msgf("downloading KubeSolo %s from %s ...", version, url)

	tmpDir, err := os.MkdirTemp(installTempParent(), "kubesolo-install-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	archivePath := filepath.Join(tmpDir, archiveName)
	if err := downloadFile(url, archivePath); err != nil {
		return fmt.Errorf("failed to download KubeSolo: %w", err)
	}

	return extractAndInstall(archivePath, tmpDir)
}

// ── offline installation ───────────────────────────────────────────────────────

func installOffline(src string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("offline source not found: %s", src)
	}

	tmpDir, err := os.MkdirTemp(installTempParent(), "kubesolo-install-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	lower := strings.ToLower(src)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		log.Info().Msgf("extracting from local archive %s...", src)
		return extractAndInstall(src, tmpDir)

	default:
		// Treat as a raw binary
		log.Info().Msgf("installing binary from %s...", src)
		tmpBin := filepath.Join(tmpDir, config.AppName)
		if err := copyFile(src, tmpBin); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
		return atomicInstall(tmpBin)
	}
}

// ── shared helpers ─────────────────────────────────────────────────────────────

// extractAndInstall unpacks a .tar.gz archive, finds the kubesolo binary inside,
// and atomically moves it to /usr/local/bin/kubesolo.
func extractAndInstall(archivePath, tmpDir string) error {
	extractedBin := filepath.Join(tmpDir, config.AppName)
	if err := extractTarGz(archivePath, tmpDir, config.AppName); err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}
	return atomicInstall(extractedBin)
}

// atomicInstall moves src to the final install path and sets executable bits.
// os.Rename is atomic on Linux when both paths are on the same filesystem;
// for cross-device installs we fall back to a copy-then-rename sequence.
func atomicInstall(src string) error {
	if err := os.MkdirAll(filepath.Dir(config.DefaultInstallPath), 0o755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	if err := os.Chmod(src, 0o755); err != nil {
		return fmt.Errorf("failed to set executable permission on binary: %w", err)
	}

	if err := os.Rename(src, config.DefaultInstallPath); err != nil {
		// Cross-device (e.g. tmpfs → ext4 on Alpine): copy then replace.
		if err2 := copyFile(src, config.DefaultInstallPath+".tmp"); err2 != nil {
			return fmt.Errorf("failed to copy binary to install path: %w", err2)
		}
		// copyFile uses os.Create which gives 0o644 — re-apply execute bits.
		if err2 := os.Chmod(config.DefaultInstallPath+".tmp", 0o755); err2 != nil {
			_ = os.Remove(config.DefaultInstallPath + ".tmp")
			return fmt.Errorf("failed to set executable permission on staged binary: %w", err2)
		}
		if err2 := os.Rename(config.DefaultInstallPath+".tmp", config.DefaultInstallPath); err2 != nil {
			_ = os.Remove(config.DefaultInstallPath + ".tmp")
			return fmt.Errorf("failed to rename binary into place: %w", err2)
		}
	}

	// Belt-and-suspenders: guarantee the installed binary is executable
	// regardless of which code path above ran (direct rename vs. copy fallback).
	if err := os.Chmod(config.DefaultInstallPath, 0o755); err != nil {
		return fmt.Errorf("failed to set executable permission on installed binary: %w", err)
	}

	log.Info().Msgf("KubeSolo installed to %s", config.DefaultInstallPath)
	return nil
}

// extractTarGz reads a .tar.gz and extracts the single named file to destDir.
func extractTarGz(archivePath, destDir, target string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("invalid gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Match the binary by base name, regardless of archive directory prefix
		if filepath.Base(hdr.Name) != target {
			continue
		}

		destPath := filepath.Join(destDir, target)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("failed to create %s: %w", destPath, err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return fmt.Errorf("failed to write %s: %w", destPath, err)
		}
		_ = out.Close()
		log.Debug().Msgf("extracted %s -> %s", hdr.Name, destPath)
		return nil
	}
	return fmt.Errorf("binary %q not found in archive", target)
}

// downloadFile fetches url and writes the body to dest, logging progress.
func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", url, resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", dest, err)
	}
	defer func() { _ = out.Close() }()

	written, err := io.Copy(out, &progressReader{r: resp.Body, total: resp.ContentLength, url: url})
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", dest, err)
	}
	log.Debug().Msgf("downloaded %d bytes from %s", written, url)
	return nil
}

// installTempParent returns the best parent directory for a temporary install
// workspace. On Alpine (and other distros) /tmp is a small tmpfs that cannot
// hold the KubeSolo binary — using $HOME (typically /root during a root
// install) puts the temp dir on the main filesystem, matching the bash
// installer's `mktemp -d -p "$HOME"` behaviour.
func installTempParent() string {
	if home := os.Getenv("HOME"); home != "" {
		if _, err := os.Stat(home); err == nil {
			return home
		}
	}
	return "" // falls back to os.TempDir()
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}

// progressReader wraps an io.Reader and logs download progress periodically.
type progressReader struct {
	r       io.Reader
	total   int64
	written int64
	last    int64
	url     string
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	p.written += int64(n)
	if p.total > 0 && p.written-p.last > 5*1024*1024 { // log every 5 MiB
		pct := float64(p.written) / float64(p.total) * 100
		log.Debug().Msgf("download progress: %.0f%% (%d / %d bytes)", pct, p.written, p.total)
		p.last = p.written
	}
	return n, err
}
