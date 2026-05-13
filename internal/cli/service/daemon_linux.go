package service

import "syscall"

// daemonSysProcAttr returns a SysProcAttr that puts the child process in its
// own process group and session so it is fully detached from the installer.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true,
	}
}
