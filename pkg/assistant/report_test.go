package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// reportStore holds one incident in each state a report tells apart.
func reportStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	at := time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)
	next := func() time.Time { at = at.Add(time.Minute); return at }

	triagedIncident(t, s, next(), "restart_component", 0.97) // awaiting approval
	approved := triagedIncident(t, s, next(), "restart_component", 0.97)
	if _, err := s.Review(approved.ID, Review{Verdict: VerdictApproved}, at); err != nil {
		t.Fatal(err)
	}
	rejected := triagedIncident(t, s, next(), "restart_component", 0.64)
	if _, err := s.Review(rejected.ID, Review{Verdict: VerdictRejected, Action: "repair_database", Note: "lease UID mismatch"}, at); err != nil {
		t.Fatal(err)
	}
	triagedIncident(t, s, next(), "restart_component", 0.64) // not offered
	triagedIncident(t, s, next(), "report_only", 1)
	skipped := next()
	if err := s.Save(Incident{ID: newIncidentID(skipped), Time: skipped, Kind: KindKubeSolo, Trigger: "kine",
		Triage: &Triage{Status: TriageSkipped, Reason: "only 2066 MiB of memory was available"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func testReporter(s *Store) *Reporter {
	return &Reporter{
		Collector: &Collector{Probes: map[string]func(context.Context) error{
			"kine":    func(context.Context) error { return nil },
			"kubelet": func(context.Context) error { return errors.New("connection refused") },
		}},
		Store: s,
		now:   func() time.Time { return time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC) },
	}
}

func TestReportStatesEachIncident(t *testing.T) {
	r, err := testReporter(reportStore(t)).Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Components["kine"] != "ok" || r.Components["kubelet"] != "connection refused" {
		t.Errorf("components = %v", r.Components)
	}
	var states []string
	for _, inc := range r.Incidents {
		states = append(states, inc.State)
	}
	// Newest first.
	want := []string{StateTriageSkipped, StateReportOnly, StateNotOffered, StateRejected, StateApproved, StateAwaitingApproval}
	if strings.Join(states, ",") != strings.Join(want, ",") {
		t.Errorf("states = %v, want %v", states, want)
	}
	if r.AwaitingApproval != 1 || r.TotalIncidents != 6 {
		t.Errorf("awaiting %d of %d, want 1 of 6", r.AwaitingApproval, r.TotalIncidents)
	}
}

// A report shows the latest incidents, not all 100.
func TestReportIsBounded(t *testing.T) {
	s := NewStore(t.TempDir())
	at := time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)
	for i := range reportIncidents + 5 {
		triagedIncident(t, s, at.Add(time.Duration(i)*time.Minute), "restart_component", 0.97)
	}
	r, err := testReporter(s).Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Incidents) != reportIncidents || r.TotalIncidents != reportIncidents+5 || r.AwaitingApproval != reportIncidents+5 {
		t.Errorf("%d incidents of %d, %d awaiting", len(r.Incidents), r.TotalIncidents, r.AwaitingApproval)
	}
}

func TestReportAsText(t *testing.T) {
	r, err := testReporter(reportStore(t)).Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	text := r.Text()
	awaiting := r.Incidents[len(r.Incidents)-1].ID
	for _, want := range []string{
		"id " + awaiting,
		"kubelet", "connection refused", // health
		"restart_component", "high", "0.97", "awaiting approval", // an offered action
		"rejected", "repair_database", "lease UID mismatch", // a review
		"skipped", "2066 MiB", // a skipped triage
		"kubesoloctl assistant approve " + awaiting, // what to do next
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text report lacks %q:\n%s", want, text)
		}
	}
}

// Incidents triaged before actions were rated have no rating to show.
func TestReportOfAnUnratedIncident(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := triagedIncident(t, s, time.Date(2026, 10, 14, 6, 29, 0, 0, time.UTC), "restart_component", 0)
	inc.Triage.Rating = ""
	if err := s.Save(inc); err != nil {
		t.Fatal(err)
	}
	r, err := testReporter(s).Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if text := r.Text(); !strings.Contains(text, "restart_component, unrated: not offered") {
		t.Errorf("text report:\n%s", text)
	}
}

// Once an approved action has been carried out, the report says how it went.
func TestReportOfACarriedOutAction(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := approved(t, s, reviewTime, "restart_component")
	if err := s.update(inc.ID, func(i *Incident) error {
		i.ActionRun = &ActionRun{Action: "restart_component", Status: ActionFailed, Reason: "did not become healthy", Start: reviewTime}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r, err := testReporter(s).Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Incidents[0].State; got != "action failed" {
		t.Errorf("state = %q, want %q", got, "action failed")
	}
	if text := r.Text(); !strings.Contains(text, "did not become healthy") {
		t.Errorf("the text report lacks the action's outcome:\n%s", text)
	}
}
