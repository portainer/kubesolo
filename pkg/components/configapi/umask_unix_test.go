//go:build unix

package configapi

import "syscall"

func setUmask(mask int) int { return syscall.Umask(mask) }
