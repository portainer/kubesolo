package assistant

import (
	"context"
	"fmt"
)

// The host checks, watched beside the control plane components: a full disk or
// exhausted memory breaks the node while every component still answers.
const (
	CheckDisk   = "disk"
	CheckMemory = "memory"

	// DiskFullPercent and MemoryLowMiB are where the checks fail, matching
	// the knowledge's thresholds.
	DiskFullPercent = 90
	MemoryLowMiB    = 200
)

// HostChecks returns the host checks for the filesystem holding dataDir, as
// Collector.Probes entries.
func HostChecks(dataDir string) map[string]func(context.Context) error {
	return hostChecks(func() HostInfo { return hostInfo(dataDir) })
}

func hostChecks(host func() HostInfo) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		CheckDisk: func(context.Context) error {
			if h := host(); h.DiskUsedPercent >= DiskFullPercent {
				return fmt.Errorf("%d%% used; %d%% or more is too full", h.DiskUsedPercent, DiskFullPercent)
			}
			return nil
		},
		CheckMemory: func(context.Context) error {
			// 0 means unknown, not exhausted.
			if h := host(); h.MemoryAvailableMiB > 0 && h.MemoryAvailableMiB < MemoryLowMiB {
				return fmt.Errorf("%d MiB available; under %d MiB is too little", h.MemoryAvailableMiB, MemoryLowMiB)
			}
			return nil
		},
	}
}
