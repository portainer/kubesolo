package filesystem

import (
	"fmt"
	"os"
)

// FileExists checks if a file exists
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// CreateSymbolicLink creates a symbolic link
func CreateSymbolicLink(source, target string) error {
	if _, err := os.Lstat(target); err == nil {
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("failed to remove existing symlink: %v", err)
		}
	} else if !os.IsNotExist(err) {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("failed to remove existing target: %v", err)
		}
	}

	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("failed to create symlink: %v", err)
	}
	return nil
}

func EnsureDirectoryExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.MkdirAll(path, 0755)
	}
	return nil
}

func EnsureSymbolicLink(source, target string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing target CNI config %s: %v", target, err)
	}

	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("failed to create symlink for CNI config %s: %v", target, err)
	}
	return nil
}
