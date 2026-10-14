//go:build !linux

package assistant

import "os/exec"

func setProcAttr(*exec.Cmd) {}
