package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testKnowledge = "## kine\nA locked database means another process holds it."

// newTestTriager returns a triager whose llama-server is the fake in the given
// mode, with a knowledge file and plenty of memory, and the file the fake
// appends each request to.
func newTestTriager(t *testing.T, mode string) (*Triager, string) {
	t.Helper()
	a, _ := newTestAssistant(t, mode)
	knowledge := filepath.Join(t.TempDir(), "knowledge.md")
	if err := os.WriteFile(knowledge, []byte(testKnowledge), 0o444); err != nil {
		t.Fatal(err)
	}
	a.SetKnowledge(knowledge, nil)
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LLAMA_REQUESTS", requests)

	now := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	return &Triager{
		Assistant:          a,
		Store:              NewStore(t.TempDir()),
		now:                func() time.Time { return now },
		memoryAvailableMiB: func() uint64 { return 3000 },
	}, requests
}

func pendingIncident(t *testing.T, s *Store, at time.Time, trigger string) Incident {
	t.Helper()
	inc := Incident{ID: newIncidentID(at), Time: at, Kind: KindKubeSolo, Trigger: trigger}
	inc.Snapshot.Components = map[string]string{"kine": "database is locked"}
	if err := s.Save(inc); err != nil {
		t.Fatal(err)
	}
	return inc
}

func triageOf(t *testing.T, s *Store, id string) *Triage {
	t.Helper()
	inc, err := s.Get(id)
	if err != nil {
		t.Fatalf("incident %s is not served: %v", id, err)
	}
	return inc.Triage
}

type sentRequest struct {
	Messages       []struct{ Role, Content string }
	ResponseFormat struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

func requestsMade(t *testing.T, file string) []sentRequest {
	t.Helper()
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []sentRequest
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r sentRequest
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// The model reads the knowledge file and the incident, and must answer in the
// schema, with an action from the fixed list.
func TestAnIncidentIsTriagedWithTheKnowledge(t *testing.T) {
	tr, requests := newTestTriager(t, "diagnose")
	inc := pendingIncident(t, tr.Store, tr.now(), "unhealthy for 5m0s: kine")

	tr.triagePending(context.Background(), tr.now())

	got := triageOf(t, tr.Store, inc.ID)
	if got.Status != TriageDone || got.Action != "repair_database" || got.Confidence != "medium" || got.Summary != "kine is down" {
		t.Errorf("triage = %+v", got)
	}
	reqs := requestsMade(t, requests)
	if len(reqs) != 1 {
		t.Fatalf("asked the model %d times, want once", len(reqs))
	}
	req := reqs[0]
	if !strings.Contains(req.Messages[0].Content, testKnowledge) {
		t.Errorf("the system prompt lacks the knowledge file: %q", req.Messages[0].Content)
	}
	if user := req.Messages[1].Content; !strings.Contains(user, "unhealthy for 5m0s: kine") || !strings.Contains(user, "database is locked") {
		t.Errorf("the prompt lacks the incident: %q", user)
	}
	if req.ResponseFormat.Type != "json_schema" {
		t.Fatalf("the answer is not constrained to the schema: %+v", req.ResponseFormat)
	}
	for _, action := range Actions {
		if !strings.Contains(string(req.ResponseFormat.JSONSchema.Schema), action) {
			t.Errorf("the schema does not offer %s", action)
		}
	}
}

// Incidents are served in the order they are triaged, so triaging oldest
// first means a poller asking for everything after the newest ID it has seen
// never misses one.
func TestIncidentsAreTriagedOldestFirst(t *testing.T) {
	tr, requests := newTestTriager(t, "diagnose")
	newer := pendingIncident(t, tr.Store, tr.now().Add(time.Minute), "newer")
	older := pendingIncident(t, tr.Store, tr.now(), "older")

	tr.triagePending(context.Background(), tr.now())

	reqs := requestsMade(t, requests)
	if len(reqs) != 2 || !strings.Contains(reqs[0].Messages[1].Content, "older") {
		t.Fatalf("triaged in the wrong order: %d requests", len(reqs))
	}
	for _, id := range []string{older.ID, newer.ID} {
		if s := triageOf(t, tr.Store, id).Status; s != TriageDone {
			t.Errorf("%s: status %s", id, s)
		}
	}
}

func TestTriageIsSkippedWhenMemoryIsShort(t *testing.T) {
	tr, requests := newTestTriager(t, "diagnose")
	tr.memoryAvailableMiB = func() uint64 { return 1200 }
	inc := pendingIncident(t, tr.Store, tr.now(), "kine")

	tr.triagePending(context.Background(), tr.now())

	got := triageOf(t, tr.Store, inc.ID)
	if got.Status != TriageSkipped || !strings.Contains(got.Reason, "1200 MiB") {
		t.Errorf("triage = %+v", got)
	}
	if reqs := requestsMade(t, requests); len(reqs) != 0 {
		t.Error("the model was started on a node short of memory")
	}
}

// While the model or the knowledge is still being fetched, incidents wait, up
// to ModelWait, and are then served as skipped.
func TestTriageWaitsForTheModel(t *testing.T) {
	tr, _ := newTestTriager(t, "diagnose")
	tr.Assistant.SetKnowledge("", nil)
	inc := pendingIncident(t, tr.Store, tr.now(), "kine")
	started := tr.now()

	tr.triagePending(context.Background(), started)
	if pending, _ := tr.Store.Pending(); len(pending) != 1 {
		t.Fatalf("the incident should wait for the knowledge, pending = %d", len(pending))
	}

	later := started.Add(ModelWait + time.Second)
	tr.now = func() time.Time { return later }
	tr.triagePending(context.Background(), started)
	got := triageOf(t, tr.Store, inc.ID)
	if got.Status != TriageSkipped || !strings.Contains(got.Reason, "knowledge") {
		t.Errorf("triage = %+v", got)
	}
}

func TestTriageIsSkippedWhenTheModelCannotBeUsed(t *testing.T) {
	tr, _ := newTestTriager(t, "diagnose")
	tr.Assistant.SetModel("", errors.New("not a model artifact"))
	inc := pendingIncident(t, tr.Store, tr.now(), "kine")

	tr.triagePending(context.Background(), tr.now())

	if got := triageOf(t, tr.Store, inc.ID); got.Status != TriageSkipped || !strings.Contains(got.Reason, "not a model artifact") {
		t.Errorf("triage = %+v", got)
	}
}

func TestATooLargeKnowledgeFileIsNotUsed(t *testing.T) {
	tr, _ := newTestTriager(t, "diagnose")
	big := filepath.Join(t.TempDir(), "knowledge.md")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", MaxKnowledgeBytes+1)), 0o444); err != nil {
		t.Fatal(err)
	}
	tr.Assistant.SetKnowledge(big, nil)
	inc := pendingIncident(t, tr.Store, tr.now(), "kine")

	tr.triagePending(context.Background(), tr.now())

	if got := triageOf(t, tr.Store, inc.ID); got.Status != TriageSkipped || !strings.Contains(got.Reason, "at most") {
		t.Errorf("triage = %+v", got)
	}
}

func TestTriageFailuresAreServed(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"crash", "exited"},
		{"off-schema", "reboot_the_node"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			tr, _ := newTestTriager(t, tc.mode)
			inc := pendingIncident(t, tr.Store, tr.now(), "kine")

			tr.triagePending(context.Background(), tr.now())

			if got := triageOf(t, tr.Store, inc.ID); got.Status != TriageFailed || !strings.Contains(got.Reason, tc.want) {
				t.Errorf("triage = %+v", got)
			}
		})
	}
}

// A triage cut short by shutdown is not a failure: the incident is triaged on
// the next start.
func TestTriageInterruptedByShutdownIsRetried(t *testing.T) {
	tr, _ := newTestTriager(t, "slow")
	pendingIncident(t, tr.Store, tr.now(), "kine")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr.triagePending(ctx, tr.now())

	if pending, _ := tr.Store.Pending(); len(pending) != 1 {
		t.Errorf("an interrupted triage should leave the incident pending, pending = %d", len(pending))
	}
}

// Lines the knowledge calls harmless are taken out before the model reads the
// incident: a small model otherwise mistakes routine startup noise for the
// cause.
func TestHarmlessLinesAreNotShownToTheModel(t *testing.T) {
	tr, requests := newTestTriager(t, "diagnose")
	knowledge := filepath.Join(t.TempDir(), "knowledge.md")
	if err := os.WriteFile(knowledge, []byte(testKnowledge+`

## Harmless lines: ignore them
- "Failed to parse etcd version", "checkpoint is not found".
- Event "InvalidDiskCapacity" at node start.

## Disk full
Signs: "no space left on device".
`), 0o444); err != nil {
		t.Fatal(err)
	}
	tr.Assistant.SetKnowledge(knowledge, nil)
	inc := Incident{ID: newIncidentID(tr.now()), Time: tr.now(), Kind: KindKubeSolo, Trigger: "kine"}
	inc.Snapshot.RecentErrors = []string{
		`apiserver: Failed to parse etcd version: could not parse "" as version`,
		"kine: write /var/lib/kubesolo/kine/state.db: no space left on device",
		"kubelet: Failed to read data from checkpoint: checkpoint is not found",
	}
	inc.Snapshot.Events = []string{"default/node/x InvalidDiskCapacity: invalid capacity 0 on image filesystem"}
	if err := tr.Store.Save(inc); err != nil {
		t.Fatal(err)
	}

	tr.triagePending(context.Background(), tr.now())

	user := requestsMade(t, requests)[0].Messages[1].Content
	for _, harmless := range []string{"etcd version", "checkpoint", "InvalidDiskCapacity"} {
		if strings.Contains(user, harmless) {
			t.Errorf("the model was shown a harmless line containing %q", harmless)
		}
	}
	if !strings.Contains(user, "no space left on device") {
		t.Error("a line the knowledge does not call harmless was dropped")
	}
	// The stored incident keeps its evidence whole.
	if got, _ := tr.Store.Get(inc.ID); len(got.Snapshot.RecentErrors) != 3 {
		t.Errorf("the stored snapshot lost lines: %q", got.Snapshot.RecentErrors)
	}
}

func TestTriageIsDeterministic(t *testing.T) {
	tr, requests := newTestTriager(t, "diagnose")
	pendingIncident(t, tr.Store, tr.now(), "kine")

	tr.triagePending(context.Background(), tr.now())

	raw, _ := os.ReadFile(requests)
	if !strings.Contains(string(raw), `"temperature":0`) {
		t.Errorf("triage should not sample: %s", raw)
	}
}
