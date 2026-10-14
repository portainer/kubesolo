package assistant

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// incidentAt is an incident that has been triaged, and so is served.
func incidentAt(t time.Time, trigger string) Incident {
	return Incident{ID: newIncidentID(t), Time: t, Kind: KindKubeSolo, Trigger: trigger,
		Triage: &Triage{Status: TriageSkipped, Reason: "test", Time: t}}
}

func TestIncidentsAreListedOldestFirstAfterACursor(t *testing.T) {
	s := NewStore(t.TempDir())
	base := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	var ids []string
	for i := range 3 {
		inc := incidentAt(base.Add(time.Duration(i)*time.Minute), fmt.Sprintf("trigger %d", i))
		if err := s.Save(inc); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inc.ID)
	}

	all, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != ids[0] || all[2].ID != ids[2] || all[1].Trigger != "trigger 1" {
		t.Fatalf("list = %+v", all)
	}

	// A poller passes the newest ID it has seen and gets only what came after.
	after, err := s.List(ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].ID != ids[2] {
		t.Errorf("since %s = %+v", ids[1], after)
	}
}

func TestIncidentRoundTrips(t *testing.T) {
	s := NewStore(t.TempDir())
	inc := incidentAt(time.Now().UTC(), "kine unhealthy for 5m0s")
	inc.Snapshot.Components = map[string]string{"kine": "dial tcp: connection refused"}
	if err := s.Save(inc); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trigger != inc.Trigger || got.Snapshot.Components["kine"] != "dial tcp: connection refused" {
		t.Errorf("got %+v", got)
	}
}

func TestOnlyTheNewestIncidentsAreKept(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	base := time.Now().UTC()
	var first string
	for i := range MaxIncidents + 5 {
		inc := incidentAt(base.Add(time.Duration(i)*time.Second), "t")
		if i == 0 {
			first = inc.ID
		}
		if err := s.Save(inc); err != nil {
			t.Fatal(err)
		}
	}

	all, _ := s.List("")
	if len(all) != MaxIncidents {
		t.Errorf("kept %d incidents, want %d", len(all), MaxIncidents)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != MaxIncidents {
		t.Errorf("%d files on disk, want %d", len(entries), MaxIncidents)
	}
	if _, err := s.Get(first); !errors.Is(err, ErrNoIncident) {
		t.Errorf("the oldest incident should be gone, got %v", err)
	}
}

// IDs come from API callers and name files, so anything that is not an ID the
// store issued must not reach the filesystem.
func TestUnknownIncidents(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"20261014T030000.000000000Z", "../../etc/passwd", ""} {
		if _, err := s.Get(id); !errors.Is(err, ErrNoIncident) {
			t.Errorf("Get(%q) = %v, want ErrNoIncident", id, err)
		}
	}
	if all, err := s.List(""); err != nil || len(all) != 0 {
		t.Errorf("an empty or missing store should list nothing, got %v, %v", all, err)
	}
}

// An incident is served only once it has been triaged, so a poller never sees
// one without its diagnosis. Until then it waits, oldest first.
func TestUntriagedIncidentsAreNotServed(t *testing.T) {
	s := NewStore(t.TempDir())
	base := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	older := Incident{ID: newIncidentID(base), Time: base, Kind: KindKubeSolo, Trigger: "older"}
	newer := Incident{ID: newIncidentID(base.Add(time.Minute)), Time: base.Add(time.Minute), Kind: KindKubeSolo, Trigger: "newer"}
	for _, inc := range []Incident{newer, older} {
		if err := s.Save(inc); err != nil {
			t.Fatal(err)
		}
	}

	if all, _ := s.List(""); len(all) != 0 {
		t.Errorf("untriaged incidents were listed: %+v", all)
	}
	if _, err := s.Get(older.ID); !errors.Is(err, ErrNoIncident) {
		t.Errorf("an untriaged incident should not be served, got %v", err)
	}
	pending, err := s.Pending()
	if err != nil || len(pending) != 2 || pending[0].Trigger != "older" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}

	older.Triage = &Triage{Status: TriageDone, Diagnosis: Diagnosis{Summary: "kine was down", Action: "report_only", Confidence: "high"}}
	if err := s.Save(older); err != nil {
		t.Fatal(err)
	}
	all, _ := s.List("")
	if len(all) != 1 || all[0].Triage == nil || all[0].Triage.Summary != "kine was down" {
		t.Errorf("a triaged incident should be listed with its triage, got %+v", all)
	}
	if pending, _ := s.Pending(); len(pending) != 1 || pending[0].Trigger != "newer" {
		t.Errorf("pending = %+v", pending)
	}
}

func TestSavingAnUntriagedIncidentSignals(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Now().UTC()
	if err := s.Save(incidentAt(now, "triaged")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Added():
		t.Fatal("saving a triaged incident should not signal")
	default:
	}
	if err := s.Save(Incident{ID: newIncidentID(now.Add(time.Second)), Time: now, Kind: KindKubeSolo, Trigger: "new"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Added():
	default:
		t.Fatal("saving a new incident should signal")
	}
}
