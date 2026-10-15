package assistant

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHostChecks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		host         HostInfo
		disk, memory string // "" means healthy, otherwise words the error carries
	}{
		{"healthy", HostInfo{DiskUsedPercent: 14, MemoryAvailableMiB: 3200}, "", ""},
		{"disk at the limit", HostInfo{DiskUsedPercent: DiskFullPercent, MemoryAvailableMiB: 3200}, "90% used", ""},
		{"disk full", HostInfo{DiskUsedPercent: 99, MemoryAvailableMiB: 3200}, "99% used", ""},
		{"memory low", HostInfo{DiskUsedPercent: 14, MemoryAvailableMiB: 120}, "", "120 MiB"},
		// A host that cannot tell, such as one without /proc/meminfo, is not
		// under pressure for want of a number.
		{"memory unknown", HostInfo{DiskUsedPercent: 14}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := hostChecks(func() HostInfo { return tc.host })
			for check, want := range map[string]string{CheckDisk: tc.disk, CheckMemory: tc.memory} {
				err := checks[check](context.Background())
				switch {
				case want == "" && err != nil:
					t.Errorf("%s: unexpected %v", check, err)
				case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
					t.Errorf("%s: got %v, want an error mentioning %q", check, err, want)
				}
			}
		})
	}
}

// A full disk is an outage like a failing component: recorded once it lasts
// the threshold.
func TestAFullDiskIsAnIncident(t *testing.T) {
	w, _, now := newWatcher(t)
	disk := HostInfo{DiskUsedPercent: 97, MemoryAvailableMiB: 3000}
	for name, check := range hostChecks(func() HostInfo { return disk }) {
		w.Collector.Probes[name] = check
	}
	ctx := context.Background()

	w.check(ctx)
	*now = now.Add(6 * time.Minute)
	w.check(ctx)

	all := listed(t, w.Store)
	if len(all) != 1 || all[0].Trigger != "unhealthy for 6m0s: disk" {
		t.Fatalf("incidents = %+v", all)
	}
	if !strings.Contains(all[0].Snapshot.Components[CheckDisk], "97% used") {
		t.Errorf("snapshot = %+v", all[0].Snapshot.Components)
	}
}
