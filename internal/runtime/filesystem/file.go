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

// EnsureDirectoryExists creates a directory if it does not exist
// it returns an error if it fails
func EnsureDirectoryExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.MkdirAll(path, 0755)
	}
	return nil
}

// EnsureSymbolicLink preserves a correct link and replaces any other target.
func EnsureSymbolicLink(source, target string) error {
	if destination, err := os.Readlink(target); err == nil && destination == source {
		return nil
	}

	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to replace symlink %s with %s: %w", target, source, err)
	}

	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("failed to install symlink %s -> %s: %w", target, source, err)
	}
	return nil
}
