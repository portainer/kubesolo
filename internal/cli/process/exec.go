package process

import "os/exec"

// newCommand creates an exec.Cmd. Kept in a separate file so tests can swap
// it out with a fake via build tags if needed.
func newCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
