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

	"github.com/rs/zerolog/log"
)

const (
	releaseBaseURL  = "https://github.com/portainer/kubesolo/releases/download"
	installerURL    = "https://get.kubesolo.io"
	binaryName      = "kubesolo"
	installDir      = "/usr/local/bin"
	installPath     = installDir + "/" + binaryName
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

// DownloadBundle fetches the release tarball and the install script into outDir
// for later offline use (mirrors --download-only from the bash script).
func DownloadBundle(outDir, archiveName, version string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	tarURL := fmt.Sprintf("%s/%s/%s", releaseBaseURL, version, archiveName)
	tarDest := filepath.Join(outDir, archiveName)
	log.Info().Msgf("downloading KubeSolo %s (%s)...", version, archiveName)
	if err := downloadFile(tarURL, tarDest); err != nil {
		return fmt.Errorf("failed to download binary archive: %w", err)
	}
	log.Info().Msgf("binary archive saved to: %s", tarDest)

	installerDest := filepath.Join(outDir, "install")
	log.Info().Msg("downloading installer...")
	if err := downloadFile(installerURL, installerDest); err != nil {
		return fmt.Errorf("failed to download installer: %w", err)
	}
	if err := os.Chmod(installerDest, 0o755); err != nil {
		return fmt.Errorf("failed to make installer executable: %w", err)
	}
	log.Info().Msgf("installer saved to: %s", installerDest)

	log.Info().Msgf(
		"download complete — transfer to the target machine and run: sudo %s --offline-install=%s",
		installerDest, tarDest,
	)
	return nil
}

// ── online installation ────────────────────────────────────────────────────────

func installOnline(archiveName, version string) error {
	url := fmt.Sprintf("%s/%s/%s", releaseBaseURL, version, archiveName)
	log.Info().Msgf("downloading KubeSolo %s from %s ...", version, url)

	tmpDir, err := os.MkdirTemp("", "kubesolo-install-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

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

	tmpDir, err := os.MkdirTemp("", "kubesolo-install-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	lower := strings.ToLower(src)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		log.Info().Msgf("extracting from local archive %s...", src)
		return extractAndInstall(src, tmpDir)

	default:
		// Treat as a raw binary
		log.Info().Msgf("installing binary from %s...", src)
		tmpBin := filepath.Join(tmpDir, binaryName)
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
	extractedBin := filepath.Join(tmpDir, binaryName)
	if err := extractTarGz(archivePath, tmpDir, binaryName); err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}
	return atomicInstall(extractedBin)
}

// atomicInstall moves src to the final install path and sets executable bits.
// os.Rename is atomic on Linux when both paths are on the same filesystem;
// for cross-device installs we fall back to a copy-then-rename sequence.
func atomicInstall(src string) error {
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	if err := os.Chmod(src, 0o755); err != nil {
		return fmt.Errorf("failed to set executable permission on binary: %w", err)
	}

	if err := os.Rename(src, installPath); err != nil {
		// Cross-device (e.g. tmpfs → ext4 on Alpine): copy then replace.
		if err2 := copyFile(src, installPath+".tmp"); err2 != nil {
			return fmt.Errorf("failed to copy binary to install path: %w", err2)
		}
		// copyFile uses os.Create which gives 0o644 — re-apply execute bits.
		if err2 := os.Chmod(installPath+".tmp", 0o755); err2 != nil {
			_ = os.Remove(installPath + ".tmp")
			return fmt.Errorf("failed to set executable permission on staged binary: %w", err2)
		}
		if err2 := os.Rename(installPath+".tmp", installPath); err2 != nil {
			_ = os.Remove(installPath + ".tmp")
			return fmt.Errorf("failed to rename binary into place: %w", err2)
		}
	}

	// Belt-and-suspenders: guarantee the installed binary is executable
	// regardless of which code path above ran (direct rename vs. copy fallback).
	if err := os.Chmod(installPath, 0o755); err != nil {
		return fmt.Errorf("failed to set executable permission on installed binary: %w", err)
	}

	log.Info().Msgf("KubeSolo installed to %s", installPath)
	return nil
}

// extractTarGz reads a .tar.gz and extracts the single named file to destDir.
func extractTarGz(archivePath, destDir, target string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("invalid gzip stream: %w", err)
	}
	defer gz.Close()

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
			out.Close()
			return fmt.Errorf("failed to write %s: %w", destPath, err)
		}
		out.Close()
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", url, resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", dest, err)
	}
	defer out.Close()

	written, err := io.Copy(out, &progressReader{r: resp.Body, total: resp.ContentLength, url: url})
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", dest, err)
	}
	log.Debug().Msgf("downloaded %d bytes from %s", written, url)
	return nil
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

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
