package filesystem

import (
	"fmt"
	"os"
)

// FileExists checks if a file exists
// it returns true if the file exists, false otherwise
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// EnsureDirectoryExists creates the directory (and any necessary parents) if it does not
// already exist. It returns an error for any failure, including permission errors that the
// previous Stat-gated implementation silently ignored.
func EnsureDirectoryExists(path string) error {
	return os.MkdirAll(path, 0755)
}

// EnsureSymbolicLink removes the existing target and creates a new symbolic link
// it returns an error if it fails
func EnsureSymbolicLink(source, target string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing target CNI config %s: %v", target, err)
	}

	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("failed to create symlink for CNI config %s: %v", target, err)
	}
	return nil
}
