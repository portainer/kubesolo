package configapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureLogs redirects zerolog for the duration of a test and returns the
// lines written. The logger is global, so this cannot run in parallel.
func captureLogs(t *testing.T, level zerolog.Level) *safeBuffer {
	t.Helper()

	buf := &safeBuffer{}
	previous, previousLevel := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(level)
	t.Cleanup(func() {
		log.Logger = previous
		zerolog.SetGlobalLevel(previousLevel)
	})

	return buf
}

// safeBuffer is written by the server's goroutine and read by the test's.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// entries returns the logged lines that came from the configuration API.
func (b *safeBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // a line from somewhere else
		}
		if entry["component"] == "configapi" && entry["message"] == "configuration API request" {
			out = append(out, entry)
		}
	}
	return out
}

func TestWriteIsLogged(t *testing.T) {
	logs := captureLogs(t, zerolog.DebugLevel)
	client, _ := api(t, nil)

	do(t, client, "PATCH", "/api/v1/config", `{"network":{"mtu":1400}}`,
		map[string]string{"Content-Type": "application/merge-patch+json"})

	entries := logs.entries(t)
	if len(entries) != 1 {
		t.Fatalf("expected one audit line, got %d:\n%s", len(entries), logs.String())
	}

	e := entries[0]
	if e["method"] != "PATCH" || e["path"] != "/api/v1/config" {
		t.Errorf("method/path = %v %v", e["method"], e["path"])
	}
	if e["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v", e["status"])
	}
	if e["level"] != "info" {
		t.Errorf("a write should be logged at info, got %v", e["level"])
	}
	changed, _ := e["changed"].([]any)
	if len(changed) != 1 || changed[0] != "network.mtu" {
		t.Errorf("changed = %v, want [network.mtu]", e["changed"])
	}
	if e["restart_required"] != true {
		t.Errorf("restart_required = %v", e["restart_required"])
	}
}

// TestSecretValuesNeverReachTheLog is the property that makes this an audit
// trail rather than a leak. Only the config path is recorded, never the value,
// so a line says the edge key was replaced without disclosing it.
func TestSecretValuesNeverReachTheLog(t *testing.T) {
	const secret = "super-secret-edge-key"

	logs := captureLogs(t, zerolog.DebugLevel)
	client, _ := api(t, nil)

	do(t, client, "PATCH", "/api/v1/config",
		`{"portainer":{"edgeKey":"`+secret+`"}}`,
		map[string]string{"Content-Type": "application/merge-patch+json"})

	if strings.Contains(logs.String(), secret) {
		t.Errorf("the edge key was written to the log:\n%s", logs.String())
	}

	entries := logs.entries(t)
	if len(entries) != 1 {
		t.Fatalf("expected one audit line, got %d", len(entries))
	}
	changed, _ := entries[0]["changed"].([]any)
	if len(changed) != 1 || changed[0] != "portainer.edgeKey" {
		t.Errorf("the line should say which setting changed, got %v", entries[0]["changed"])
	}
}

// TestReadsAreLoggedAtDebug keeps a polling user interface from burying the
// changes among its own reads.
func TestReadsAreLoggedAtDebug(t *testing.T) {
	logs := captureLogs(t, zerolog.DebugLevel)
	client, _ := api(t, nil)

	do(t, client, "GET", "/api/v1/config", nil, nil)

	entries := logs.entries(t)
	if len(entries) != 1 {
		t.Fatalf("expected one line, got %d", len(entries))
	}
	if entries[0]["level"] != "debug" {
		t.Errorf("a read should be logged at debug, got %v", entries[0]["level"])
	}
}

func TestReadsAreSilentAtInfo(t *testing.T) {
	logs := captureLogs(t, zerolog.InfoLevel)
	client, _ := api(t, nil)

	do(t, client, "GET", "/api/v1/config", nil, nil)

	if entries := logs.entries(t); len(entries) != 0 {
		t.Errorf("reads must not appear at info level, got %v", entries)
	}
}

// TestHealthChecksAreNotLogged — they carry no information and would be the
// loudest thing in the journal.
func TestHealthChecksAreNotLogged(t *testing.T) {
	logs := captureLogs(t, zerolog.DebugLevel)
	client, _ := api(t, nil)

	do(t, client, "GET", "/healthz", nil, nil)

	if entries := logs.entries(t); len(entries) != 0 {
		t.Errorf("health checks must not be logged, got %v", entries)
	}
}

// TestRejectionsAreLoggedAtWarn — a refused change is worth seeing whatever it
// was trying to do, and the reason must match what the client was told.
func TestRejectionsAreLoggedAtWarn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		patch      string
		wantStatus int
		wantReason string
	}{
		{"immutable setting", `{"path":"/somewhere/else"}`, http.StatusConflict, "cannot be changed"},
		{"invalid value", `{"kubernetes":{"kubelet":{"cpuManager":{"policy":"dynamic"}}}}`, http.StatusUnprocessableEntity, "cpu-manager-policy"},
		{"malformed body", `{not json`, http.StatusBadRequest, "not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t, zerolog.DebugLevel)
			client, _ := api(t, nil)

			do(t, client, "PATCH", "/api/v1/config", tc.patch,
				map[string]string{"Content-Type": "application/merge-patch+json"})

			entries := logs.entries(t)
			if len(entries) != 1 {
				t.Fatalf("expected one line, got %d:\n%s", len(entries), logs.String())
			}
			e := entries[0]
			if e["level"] != "warn" {
				t.Errorf("a rejection should be logged at warn, got %v", e["level"])
			}
			if e["status"] != float64(tc.wantStatus) {
				t.Errorf("status = %v, want %d", e["status"], tc.wantStatus)
			}
			reason, _ := e["reason"].(string)
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.wantReason)
			}
			if _, ok := e["changed"]; ok {
				t.Error("a rejected request changed nothing and must not claim otherwise")
			}
		})
	}
}

// TestNoOpWriteLogsNoChanges — a write that alters nothing should say so,
// rather than implying a restart is needed.
func TestNoOpWriteLogsNoChanges(t *testing.T) {
	logs := captureLogs(t, zerolog.DebugLevel)
	client, _ := api(t, func(c *types.Config) { c.Network.MTU = 1400 })

	do(t, client, "PATCH", "/api/v1/config", `{"network":{"mtu":1400}}`,
		map[string]string{"Content-Type": "application/merge-patch+json"})

	entries := logs.entries(t)
	if len(entries) != 1 {
		t.Fatalf("expected one line, got %d", len(entries))
	}
	if _, ok := entries[0]["changed"]; ok {
		t.Errorf("nothing changed, so no changed list should appear: %v", entries[0])
	}
}
