package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portainer/kubesolo/pkg/assistant"
)

type fakeIncidents map[string]*assistant.Incident

func (f fakeIncidents) Incidents(string) ([]assistant.Summary, error) {
	var out []assistant.Summary
	for _, inc := range f {
		out = append(out, assistant.Summary{ID: inc.ID, Triage: inc.Triage, Review: inc.Review})
	}
	return out, nil
}

func (f fakeIncidents) Incident(id string) (*assistant.Incident, error) { return f[id], nil }

// Each reviewed incident that names the right action is written as a test
// case; the rest are not labels.
func TestExportWritesReviewedIncidentsAsCases(t *testing.T) {
	at := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	triage := &assistant.Triage{Status: assistant.TriageDone, Diagnosis: assistant.Diagnosis{Action: "report_only"}}
	f := fakeIncidents{
		"a": {ID: "a", Time: at, Trigger: "kine", Triage: triage, Review: &assistant.Review{Verdict: assistant.VerdictRejected, Action: "repair_database"}},
		"b": {ID: "b", Time: at, Trigger: "kubelet", Triage: triage, Review: &assistant.Review{Verdict: assistant.VerdictRejected}},
		"c": {ID: "c", Time: at, Trigger: "killed", Triage: triage},
	}
	dir := t.TempDir()

	n, err := exportCases(f, dir, "edge-01")
	if err != nil || n != 1 {
		t.Fatalf("exported %d, %v; want 1", n, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "a.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c assistant.Case
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Incident.Trigger != "kine" || c.Expect.Actions[0] != "repair_database" || c.Source.Node != "edge-01" {
		t.Errorf("case = %+v", c)
	}
}
