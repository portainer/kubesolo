package assistant

import (
	"os/exec"
	"syscall"
)

// setProcAttr has the kernel kill llama-server if KubeSolo dies, so that a
// crash never leaves the model holding memory.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
