package upgrade

import (
	"os"
	"strconv"
	"strings"
)

// RecordStarted notes that this process has finished starting: every
// component is up and every add-on, the Portainer agent among them, has been
// deployed from raw, the configuration file it started with. It is readable
// by root only: it holds the edge key.
func RecordStarted(l Layout, raw []byte) error {
	if err := WriteFileAtomic(l.StartedConfig(), raw, 0o600); err != nil {
		return err
	}
	return WriteFileAtomic(l.StartedFile(), []byte(strconv.Itoa(os.Getpid())), 0o600)
}

// StartedPID is the PID RecordStarted last recorded, or 0.
func StartedPID(l Layout) int {
	raw, err := os.ReadFile(l.StartedFile())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}
