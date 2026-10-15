package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/portainer/kubesolo/internal/cli/dockerhost"
)

// LockInstance takes the lock that serialises the commands which replace the
// named instance's container — install, upgrade, rollback, reset, uninstall —
// so that two of them cannot interleave their stops, backups and replacements.
// It fails at once when another holds it, and returns the function that
// releases it. The lock is a file in this user's cache directory, keyed by the
// engine and the container: it serialises kubesoloctl on this machine, not
// clients of a remote engine elsewhere.
func LockInstance(name string) (func(), error) {
	host, err := dockerhost.Host()
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	dir := filepath.Join(cache, "kubesoloctl", "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(host))
	path := filepath.Join(dir, hex.EncodeToString(sum[:8])+"-"+ContainerNameFor(name)+".lock")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another kubesoloctl is changing %s; wait for it to finish", ContainerNameFor(name))
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
