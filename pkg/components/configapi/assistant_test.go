package configapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/pkg/assistant"
)

type fakeAsker func(ctx context.Context, question string) (string, error)

func (f fakeAsker) Ask(ctx context.Context, question string) (string, error) { return f(ctx, question) }

// assistantAPI runs the API with the given assistant, nil meaning disabled, and
// returns a client for it.
func assistantAPI(t *testing.T, asker Asker) *Client {
	return assistantAPIWith(t, asker, nil)
}

func assistantAPIWith(t *testing.T, asker Asker, incidents Incidents) *Client {
	t.Helper()
	dir := shortTempDir(t)
	configPath := filepath.Join(dir, "config.yaml")
	if err := config.Write(configPath, config.Defaults()); err != nil {
		t.Fatal(err)
	}

	socket := filepath.Join(dir, "c.sock")
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, cancel, make(chan struct{}), Options{
		SocketPath: socket,
		ConfigPath: configPath,
		Host:       config.Host{NumCPU: 4, GOARCH: "amd64"},
		Assistant:  asker,
		Incidents:  incidents,
	})
	done := make(chan error, 1)
	go func() { done <- svc.Run() }()
	<-svc.readyCh
	t.Cleanup(func() { cancel(); <-done })

	return NewClient(socket)
}

func TestAskReturnsTheAnswer(t *testing.T) {
	c := assistantAPI(t, fakeAsker(func(_ context.Context, q string) (string, error) {
		return "answer to: " + q, nil
	}))

	got, err := c.Ask("why is the disk full?")
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "answer to: why is the disk full?" {
		t.Errorf("answer = %q", got.Answer)
	}
}

func TestAskWhileBusyIsAConflict(t *testing.T) {
	c := assistantAPI(t, fakeAsker(func(context.Context, string) (string, error) {
		return "", assistant.ErrBusy
	}))

	_, err := c.Ask("hello")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("expected 409, got %v", err)
	}
}

func TestAskFailureIsReported(t *testing.T) {
	c := assistantAPI(t, fakeAsker(func(context.Context, string) (string, error) {
		return "", errors.New("model /x.gguf cannot be read")
	}))

	if _, err := c.Ask("hello"); err == nil || !strings.Contains(err.Error(), "/x.gguf") {
		t.Fatalf("expected the cause to reach the caller, got %v", err)
	}
}

func TestAskWhenTheAssistantIsDisabled(t *testing.T) {
	c := assistantAPI(t, nil)

	_, err := c.Ask("hello")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || !strings.Contains(err.Error(), "assistant.enabled") {
		t.Fatalf("expected a 404 telling how to enable it, got %v", err)
	}
}

func TestAskNeedsAQuestion(t *testing.T) {
	c := assistantAPI(t, fakeAsker(func(context.Context, string) (string, error) {
		t.Error("an empty question reached the assistant")
		return "", nil
	}))

	_, err := c.Ask("  ")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %v", err)
	}
}

// An answer takes far longer than the server's write timeout; the response
// must still reach the caller.
func TestAskOutlastsTheWriteTimeout(t *testing.T) {
	c := assistantAPI(t, fakeAsker(func(context.Context, string) (string, error) {
		time.Sleep(httpWriteTimeout + time.Second)
		return "slow answer", nil
	}))

	got, err := c.Ask("hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "slow answer" {
		t.Errorf("answer = %q", got.Answer)
	}
}

func incidentsAPI(t *testing.T) (*Client, *assistant.Store, []string) {
	t.Helper()
	store := assistant.NewStore(t.TempDir())
	base := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	var ids []string
	for i, trigger := range []string{"unhealthy for 5m0s: kine", "KubeSolo did not shut down cleanly"} {
		at := base.Add(time.Duration(i) * time.Hour)
		inc := assistant.Incident{ID: at.Format("20060102T150405.000000000Z"), Time: at, Kind: assistant.KindKubeSolo, Trigger: trigger,
			Triage: &assistant.Triage{Status: assistant.TriageSkipped, Reason: "test"}}
		inc.Snapshot.Components = map[string]string{"kine": "connection refused"}
		if err := store.Save(inc); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inc.ID)
	}
	return assistantAPIWith(t, nil, store), store, ids
}

func TestIncidentsArePolledAfterACursor(t *testing.T) {
	c, _, ids := incidentsAPI(t)

	all, err := c.Incidents("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != ids[0] || all[1].Trigger != "KubeSolo did not shut down cleanly" {
		t.Fatalf("incidents = %+v", all)
	}

	newer, err := c.Incidents(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(newer) != 1 || newer[0].ID != ids[1] {
		t.Errorf("since %s = %+v", ids[0], newer)
	}

	none, err := c.Incidents(ids[1])
	if err != nil || len(none) != 0 {
		t.Errorf("nothing new should be an empty list, got %+v, %v", none, err)
	}
}

func TestIncidentIsReadWhole(t *testing.T) {
	c, _, ids := incidentsAPI(t)

	inc, err := c.Incident(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if inc.Snapshot.Components["kine"] != "connection refused" {
		t.Errorf("incident = %+v", inc)
	}

	_, err = c.Incident("20991231T000000.000000000Z")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("an unknown incident should be 404, got %v", err)
	}
}

func TestIncidentsWhenTheAssistantIsDisabled(t *testing.T) {
	c := assistantAPIWith(t, nil, nil)

	_, err := c.Incidents("")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || !strings.Contains(err.Error(), "assistant.enabled") {
		t.Fatalf("expected a 404 telling how to enable it, got %v", err)
	}
}

// reviewAPI serves a store holding a high-rated restart and a medium-rated
// one.
func reviewAPI(t *testing.T) (c *Client, high, medium string) {
	t.Helper()
	store := assistant.NewStore(t.TempDir())
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var ids []string
	for i, p := range []float64{0.97, 0.75} {
		at := base.Add(time.Duration(i) * time.Minute)
		rating := assistant.RatingHigh
		if p < assistant.ActionThreshold {
			rating = assistant.RatingMedium
		}
		inc := assistant.Incident{ID: at.Format("20060102T150405.000000000Z"), Time: at, Kind: assistant.KindKubeSolo, Trigger: "unhealthy for 5m0s: kubelet",
			Triage: &assistant.Triage{Status: assistant.TriageDone, Diagnosis: assistant.Diagnosis{Summary: "s", Action: "restart_component", ActionProbability: p, Rating: rating}}}
		if err := store.Save(inc); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inc.ID)
	}
	return assistantAPIWith(t, nil, store), ids[0], ids[1]
}

func TestApproveAndRejectOverTheAPI(t *testing.T) {
	c, high, medium := reviewAPI(t)

	inc, err := c.Approve(high, "kubelet was wedged")
	if err != nil {
		t.Fatal(err)
	}
	if r := inc.Review; r == nil || r.Verdict != assistant.VerdictApproved || r.Action != "restart_component" || r.Note != "kubelet was wedged" {
		t.Errorf("review = %+v", inc.Review)
	}

	inc, err = c.Reject(medium, "clean_orphaned_containers", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := inc.Review; r == nil || r.Verdict != assistant.VerdictRejected || r.Action != "clean_orphaned_containers" {
		t.Errorf("review = %+v", inc.Review)
	}

	// Polled incidents carry their review.
	list, _ := c.Incidents("")
	if len(list) != 2 || list[0].Review == nil || list[1].Review == nil {
		t.Errorf("incidents = %+v", list)
	}
}

func TestReviewsRefusedOverTheAPI(t *testing.T) {
	c, high, medium := reviewAPI(t)
	for _, tc := range []struct {
		name   string
		review func() error
		status int
	}{
		{"approving what is not offered", func() error { _, err := c.Approve(medium, ""); return err }, http.StatusConflict},
		{"an unknown action", func() error { _, err := c.Reject(high, "reboot", ""); return err }, http.StatusBadRequest},
		{"an unknown incident", func() error { _, err := c.Approve("20991231T000000.000000000Z", ""); return err }, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var apiErr *APIError
			if err := tc.review(); !errors.As(err, &apiErr) || apiErr.Status != tc.status {
				t.Errorf("got %v, want status %d", err, tc.status)
			}
		})
	}

	// Only approve and reject are verbs on an incident.
	var apiErr *APIError
	if err := c.doJSON(http.MethodPost, "/api/v1/assistant/incidents/"+high+":delete", nil, "", nil); !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("an unknown verb: got %v, want 404", err)
	}
}

func TestReviewsWhenTheAssistantIsDisabled(t *testing.T) {
	c := assistantAPIWith(t, nil, nil)
	var apiErr *APIError
	if _, err := c.Approve("20261015T090000.000000000Z", ""); !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || !strings.Contains(err.Error(), "assistant.enabled") {
		t.Errorf("expected a 404 telling how to enable it, got %v", err)
	}
}
