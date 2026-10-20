package assistant

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// triagedIncident saves an incident triaged with the given action and
// probability.
func triagedIncident(t *testing.T, s *Store, at time.Time, action string, p float64) Incident {
	t.Helper()
	inc := Incident{ID: newIncidentID(at), Time: at, Kind: KindKubeSolo, Trigger: "unhealthy for 5m0s: kubelet",
		Triage: &Triage{Status: TriageDone, Time: at, Diagnosis: Diagnosis{
			Summary: "The kubelet stopped answering.", Action: action, ActionProbability: p, Rating: rating(p)}}}
	if err := s.Save(inc); err != nil {
		t.Fatal(err)
	}
	return inc
}

var reviewTime = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// Approving agrees with the suggested action, which becomes the label.
func TestApprovingAnOfferedAction(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := triagedIncident(t, s, reviewTime, "restart_component", 0.97)

	got, err := s.Review(inc.ID, Review{Verdict: VerdictApproved, Note: "kubelet was wedged"}, reviewTime)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Review
	if r == nil || r.Verdict != VerdictApproved || r.Action != "restart_component" || r.Note != "kubelet was wedged" || !r.Time.Equal(reviewTime) {
		t.Errorf("review = %+v", r)
	}
	if stored, _ := s.Get(inc.ID); stored.Review == nil || stored.Review.Action != "restart_component" {
		t.Errorf("the review was not saved: %+v", stored.Review)
	}
}

// Only an action that is offered — rated high, and something to do — can be
// approved: approval will be what lets KubeSolo act.
func TestApprovingWhatIsNotOffered(t *testing.T) {
	s := NewStore(t.TempDir())
	for i, tc := range []struct {
		action string
		p      float64
	}{
		{"restart_component", 0.75}, // medium
		{"report_only", 1},          // nothing to do
	} {
		inc := triagedIncident(t, s, reviewTime.Add(time.Duration(i)*time.Second), tc.action, tc.p)
		if _, err := s.Review(inc.ID, Review{Verdict: VerdictApproved}, reviewTime); !errors.Is(err, ErrNotOffered) {
			t.Errorf("%s at %.2f: got %v, want ErrNotOffered", tc.action, tc.p, err)
		}
	}
}

// Rejecting says the suggestion was wrong, and can say what was right: a
// missed fix behind report_only is worth as much as a wrong action.
func TestRejectingRecordsTheRightAction(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := triagedIncident(t, s, reviewTime, "report_only", 0.76)

	got, err := s.Review(inc.ID, Review{Verdict: VerdictRejected, Action: "repair_database", Note: "lease UID mismatch"}, reviewTime)
	if err != nil {
		t.Fatal(err)
	}
	if r := got.Review; r.Verdict != VerdictRejected || r.Action != "repair_database" {
		t.Errorf("review = %+v", r)
	}
}

func TestReviewsThatAreRefused(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := triagedIncident(t, s, reviewTime, "restart_component", 0.97)
	skipped := Incident{ID: newIncidentID(reviewTime.Add(time.Second)), Time: reviewTime, Kind: KindKubeSolo, Trigger: "kine",
		Triage: &Triage{Status: TriageSkipped, Reason: "low memory"}}
	if err := s.Save(skipped); err != nil {
		t.Fatal(err)
	}
	pending := Incident{ID: newIncidentID(reviewTime.Add(2 * time.Second)), Time: reviewTime, Kind: KindKubeSolo, Trigger: "kine"}
	if err := s.Save(pending); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		id   string
		r    Review
		want error
		msg  string
	}{
		{"unknown verdict", inc.ID, Review{Verdict: "maybe"}, ErrInvalidReview, "verdict"},
		{"unknown action", inc.ID, Review{Verdict: VerdictRejected, Action: "reboot"}, ErrInvalidReview, "reboot"},
		{"approving another action", inc.ID, Review{Verdict: VerdictApproved, Action: "repair_database"}, ErrInvalidReview, "suggested"},
		{"long note", inc.ID, Review{Verdict: VerdictRejected, Note: strings.Repeat("x", maxNoteLength+1)}, ErrInvalidReview, "note"},
		{"approving what was not diagnosed", skipped.ID, Review{Verdict: VerdictApproved}, ErrNotOffered, "skipped"},
		{"not triaged yet", pending.ID, Review{Verdict: VerdictRejected}, ErrNoIncident, ""},
		{"unknown incident", "20991231T000000.000000000Z", Review{Verdict: VerdictRejected}, ErrNoIncident, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Review(tc.id, tc.r, reviewTime)
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("got %v, want %v mentioning %q", err, tc.want, tc.msg)
			}
		})
	}
}

// A second review replaces the first: people change their minds once they
// learn more.
func TestAReviewCanBeRevised(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := triagedIncident(t, s, reviewTime, "restart_component", 0.97)
	if _, err := s.Review(inc.ID, Review{Verdict: VerdictApproved}, reviewTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.Review(inc.ID, Review{Verdict: VerdictRejected, Action: "clean_orphaned_containers"}, reviewTime.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Review.Verdict != VerdictRejected || got.Review.Action != "clean_orphaned_containers" {
		t.Errorf("review = %+v", got.Review)
	}
}

// An incident the model could not diagnose can still be labelled with the
// right action: those are the cases that show where triage falls short.
func TestRejectingAnUndiagnosedIncident(t *testing.T) {
	s := NewStore(t.TempDir())
	skipped := Incident{ID: newIncidentID(reviewTime), Time: reviewTime, Kind: KindKubeSolo, Trigger: "kine",
		Triage: &Triage{Status: TriageSkipped, Reason: "low memory"}}
	if err := s.Save(skipped); err != nil {
		t.Fatal(err)
	}
	got, err := s.Review(skipped.ID, Review{Verdict: VerdictRejected, Action: "restart_component"}, reviewTime)
	if err != nil || got.Review.Action != "restart_component" {
		t.Errorf("got %+v, %v", got, err)
	}
}

// Reviewed incidents are the labelled data, so pruning keeps them: the
// oldest unreviewed incidents go first, and reviewed ones only past their own,
// larger bound.
func TestReviewedIncidentsOutlastPruning(t *testing.T) {
	s := NewStore(t.TempDir())
	reviewed := triagedIncident(t, s, reviewTime, "restart_component", 0.97)
	if _, err := s.Review(reviewed.ID, Review{Verdict: VerdictApproved}, reviewTime); err != nil {
		t.Fatal(err)
	}
	for i := range MaxIncidents + 5 {
		triagedIncident(t, s, reviewTime.Add(time.Duration(i+1)*time.Second), "report_only", 1)
	}

	if _, err := s.Get(reviewed.ID); err != nil {
		t.Errorf("the reviewed incident was pruned: %v", err)
	}
	all, _ := s.List("")
	if len(all) != MaxIncidents+1 {
		t.Errorf("kept %d incidents, want %d unreviewed and the reviewed one", len(all), MaxIncidents)
	}
}

func TestReviewedIncidentsAreBounded(t *testing.T) {
	s := NewStore(t.TempDir())
	var first string
	for i := range MaxReviewedIncidents + 2 {
		inc := triagedIncident(t, s, reviewTime.Add(time.Duration(i)*time.Second), "report_only", 1)
		if _, err := s.Review(inc.ID, Review{Verdict: VerdictRejected, Action: "restart_component"}, reviewTime); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = inc.ID
		}
	}
	if _, err := s.Get(first); !errors.Is(err, ErrNoIncident) {
		t.Errorf("the oldest reviewed incident should go past %d, got %v", MaxReviewedIncidents, err)
	}
}

// A reviewed incident becomes a test case labelled with the reviewer's
// action, in the test set's format.
func TestAReviewedIncidentIsATestCase(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := triagedIncident(t, s, reviewTime, "report_only", 0.76)
	got, err := s.Review(inc.ID, Review{Verdict: VerdictRejected, Action: "repair_database", Note: "lease UID mismatch"}, reviewTime)
	if err != nil {
		t.Fatal(err)
	}

	c, ok := CaseFromReview(*got)
	if !ok || c.Incident.Trigger != inc.Trigger || len(c.Expect.Actions) != 1 || c.Expect.Actions[0] != "repair_database" {
		t.Errorf("case = %+v, %v", c, ok)
	}
	if c.Source.Review == nil || c.Source.Triage == nil {
		t.Errorf("the case should keep the review and the triage it came from: %+v", c.Source)
	}

	// A rejection that names no action says what was wrong, not what was
	// right: it is no label.
	unlabelled, _ := s.Review(inc.ID, Review{Verdict: VerdictRejected}, reviewTime)
	if _, ok := CaseFromReview(*unlabelled); ok {
		t.Error("a review without an action should not make a case")
	}
}
