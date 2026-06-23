//go:build !linux

package service

import "syscall"

// daemonSysProcAttr returns nil on non-Linux platforms. The installer only
// runs on Linux in practice; this stub exists solely to satisfy the compiler
// when building or running tests on macOS/Windows development machines.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return nil
}
