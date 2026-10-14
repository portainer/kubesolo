package assistant

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func incidentAt(t time.Time, trigger string) Incident {
	return Incident{ID: newIncidentID(t), Time: t, Kind: KindKubeSolo, Trigger: trigger}
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
