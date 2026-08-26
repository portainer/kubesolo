//go:build unix

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriteModeIgnoresUmask covers the one thing the explicit Chmod in
// writeAndClose buys over os.CreateTemp's own behaviour.
//
// CreateTemp documents mode 0600, but that is before umask, and umask subtracts
// bits: under 0277 it yields 0400, under 0677 it yields 0000. Rename carries
// whatever mode the temporary file had onto the final path, so without the Chmod
// the config file's mode would depend on the umask KubeSolo happened to inherit.
//
// The direction only ever goes one way — umask cannot add permissions — so this
// is about the file staying writable, not about it staying private.
func TestWriteModeIgnoresUmask(t *testing.T) {
	for _, umask := range []int{0o022, 0o077, 0o277, 0o677} {
		t.Run(maskName(umask), func(t *testing.T) {
			dir := t.TempDir()

			old := syscall.Umask(umask)
			t.Cleanup(func() { syscall.Umask(old) })

			// The directory itself must survive the umask, or the write fails for
			// an unrelated reason.
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(dir, "config.yaml")
			if err := Write(path, Defaults()); err != nil {
				t.Fatal(err)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != configFileMode {
				t.Errorf("mode = %04o under umask %04o, want %04o", got, umask, configFileMode)
			}
		})
	}
}

func maskName(umask int) string { return fmt.Sprintf("umask_%04o", umask) }
