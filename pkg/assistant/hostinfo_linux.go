package assistant

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func hostInfo(dataDir string) HostInfo {
	var h HostInfo
	var fs syscall.Statfs_t
	if syscall.Statfs(dataDir, &fs) == nil && fs.Blocks > 0 {
		h.DiskUsedPercent = int(100 - fs.Bavail*100/fs.Blocks)
	}
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if f := strings.Fields(line); len(f) == 3 && f[0] == "MemAvailable:" {
				kb, _ := strconv.ParseUint(f[1], 10, 64)
				h.MemoryAvailableMiB = kb / 1024
			}
		}
	}
	if raw, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 0 {
			secs, _ := strconv.ParseFloat(f[0], 64)
			h.UptimeSeconds = int64(secs)
		}
	}
	return h
}
