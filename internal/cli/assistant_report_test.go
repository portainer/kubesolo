package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/portainer/kubesolo/pkg/assistant"
)

// The report reads like the other commands, and says exactly what to type to
// approve or reject each action awaiting approval.
func TestPrintReport(t *testing.T) {
	const id = "20261027T035947.994855809Z"
	r := assistant.Report{
		Time:       time.Date(2026, 10, 27, 4, 5, 0, 0, time.UTC),
		Components: map[string]string{"kine": "ok", "kubelet": "connection refused"},
		Host:       assistant.HostInfo{DiskUsedPercent: 23, MemoryAvailableMiB: 7138, UptimeSeconds: 2132},
		Incidents: []assistant.ReportIncident{{
			Summary: assistant.Summary{
				ID: id, Time: time.Date(2026, 10, 27, 3, 59, 47, 0, time.UTC), Trigger: "unhealthy for 5m30s: kubelet",
				Triage: &assistant.Triage{Status: assistant.TriageDone, Diagnosis: assistant.Diagnosis{
					Summary: "The kubelet is unhealthy.", Action: "restart_component", ActionProbability: 0.95, Rating: assistant.RatingHigh,
				}},
			},
			State: assistant.StateAwaitingApproval,
		}},
		TotalIncidents:   1,
		AwaitingApproval: 1,
	}
	var b bytes.Buffer
	printReport(ui.NewWriter(&b), r)
	out := b.String()
	for _, want := range []string{
		"kubesoloctl  assistant report",
		"[ok] kine",
		"[fail] kubelet: connection refused",
		"disk 23% used, 7138 MiB memory available, up 35m32s",
		"> unhealthy for 5m30s: kubelet",
		"ID       " + id,
		"Action   restart_component, rated high (0.95)",
		"State    awaiting approval",
		"sudo kubesoloctl assistant approve " + id,
		"sudo kubesoloctl assistant reject " + id + " --action <the right action>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}
