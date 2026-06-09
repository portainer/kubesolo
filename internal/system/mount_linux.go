//go:build linux

package system

import (
	"fmt"
	"syscall"
)

func mountMakeRShared() error {
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_SHARED, ""); err != nil {
		return fmt.Errorf("failed to make / rshared: %w", err)
	}
	return nil
}
