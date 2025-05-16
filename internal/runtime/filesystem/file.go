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
