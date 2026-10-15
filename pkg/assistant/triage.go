package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Actions are what the model may suggest. None is taken: a suggestion is only
// reported.
var Actions = []string{
	"restart_component",
	"repair_database",
	"prune_images",
	"clean_orphaned_containers",
	"free_disk_space",
	"report_only",
}

// Ratings of an action's probability. An action is trusted enough to act on
// only at ActionThreshold, the top of the high rating.
const (
	RatingHigh   = "high"
	RatingMedium = "medium"
	RatingLow    = "low"

	// ActionThreshold is the probability at which an action is rated high. On
	// the test set no false action reached it.
	ActionThreshold = 0.8
)

// rating puts an action's probability in words. An unknown probability, 0,
// is low.
func rating(p float64) string {
	switch {
	case p >= ActionThreshold:
		return RatingHigh
	case p >= 0.5:
		return RatingMedium
	}
	return RatingLow
}

const (
	// MaxKnowledgeBytes is the knowledge file's share of the model's prompt:
	// about 3,000 of the default 8,192 tokens, as much as the snapshot.
	MaxKnowledgeBytes = 12 << 10

	// TriageMinMemoryMiB is the memory available below which an incident is
	// not triaged: the model holds about 2 GB, and the node's workloads need
	// room beside it.
	TriageMinMemoryMiB = 2560

	// ModelWait is how long incidents wait for the model and the knowledge to
	// be fetched before they are served untriaged.
	ModelWait = time.Hour

	// triageTimeout bounds one triage: loading the model, reading a prompt of
	// several thousand tokens on two CPUs, and writing the diagnosis.
	triageTimeout = 15 * time.Minute

	// maxDiagnosisTokens bounds the answer, which is a few sentences.
	maxDiagnosisTokens = 512

	// actionAlternatives is how many alternatives llama-server reports at each
	// token: enough to cover the six actions and the words that compete with
	// them.
	actionAlternatives = 10
)

const triagePrompt = `You are the KubeSolo assistant. KubeSolo is a single-node Kubernetes distribution for edge devices: one binary running containerd, kine (SQLite in place of etcd), the API server, the controller manager, the scheduler, the kubelet and CoreDNS.

Triage the incident you are given, using the knowledge below. Answer with:
- summary: one sentence on what went wrong;
- cause: the most likely cause, and the knowledge entry it matches;
- action: the Action that entry gives for this evidence; report_only when no entry matches or a person must act.

# Knowledge

`

// diagnosisFormat constrains the model's answer to a Diagnosis with an action
// from the fixed list.
var diagnosisFormat = map[string]any{
	"type":        "json_schema",
	"json_schema": map[string]any{"name": "diagnosis", "schema": diagnosisSchema},
}

// diagnosisSchema is written out rather than built from a map, whose keys
// encoding/json sorts: the model writes the fields in the schema's order, and
// must reach the action only after the summary and the cause.
var diagnosisSchema = json.RawMessage(`{"type":"object","properties":{` +
	`"summary":{"type":"string","maxLength":400},` +
	`"cause":{"type":"string","maxLength":400},` +
	`"action":{"type":"string","enum":` + mustJSON(Actions) + `}},` +
	`"required":["summary","cause","action"],"additionalProperties":false}`)

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// Diagnose asks the model to triage the incident with the knowledge file. It
// waits for a question being answered, rather than failing as Ask does.
func (a *Assistant) Diagnose(ctx context.Context, inc Incident) (Diagnosis, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	m, k := a.files()
	model, err := m.get("model")
	if err != nil {
		return Diagnosis{}, err
	}
	path, err := k.get("knowledge")
	if err != nil {
		return Diagnosis{}, err
	}
	knowledge, err := os.ReadFile(path)
	if err != nil {
		return Diagnosis{}, fmt.Errorf("the knowledge is %w: %w", ErrUnavailable, err)
	}
	if len(knowledge) > MaxKnowledgeBytes {
		return Diagnosis{}, fmt.Errorf("the knowledge is %w: %s is %d bytes, and can be at most %d", ErrUnavailable, path, len(knowledge), MaxKnowledgeBytes)
	}

	// A small model mistakes routine startup noise for the cause, so the lines
	// the knowledge calls harmless are taken out first.
	snap := inc.Snapshot
	ignore := harmless(string(knowledge))
	snap.RecentErrors = without(snap.RecentErrors, ignore)
	snap.Events = without(snap.Events, ignore)
	evidence, err := json.Marshal(struct {
		Trigger  string    `json:"trigger"`
		Time     time.Time `json:"time"`
		Snapshot Snapshot  `json:"snapshot"`
	}{inc.Trigger, inc.Time, snap})
	if err != nil {
		return Diagnosis{}, err
	}

	ans, err := a.run(ctx, model, chatRequest{
		Messages: []message{
			{Role: "system", Content: triagePrompt + string(knowledge)},
			{Role: "user", Content: string(evidence)},
		},
		ResponseFormat: diagnosisFormat,
		MaxTokens:      maxDiagnosisTokens,
		// The same incident should get the same diagnosis.
		Temperature: new(float64),
		Logprobs:    true,
		TopLogprobs: actionAlternatives,
	})
	if err != nil {
		return Diagnosis{}, err
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(ans.content), &d); err != nil || d.Summary == "" ||
		!slices.Contains(Actions, d.Action) {
		return Diagnosis{}, fmt.Errorf("the model answered outside the schema: %s", ans.content)
	}
	if p, ok := actionProbability(ans.content, d.Action, ans.tokens); ok {
		d.ActionProbability = p
	}
	d.Rating = rating(d.ActionProbability)
	return d, nil
}

// actionProbability returns the probability the model gave action among the
// six, read from the alternatives it weighed where the action starts. Each
// action begins with a different token; an alternative that could begin
// several is not counted. It reports false when the tokens do not contain the
// answer or name none of the actions.
func actionProbability(content, action string, tokens []tokenLogprob) (float64, bool) {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteString(t.Token)
	}
	// The tokens can carry more than the message, such as an empty reasoning
	// block before it.
	base := strings.Index(b.String(), content)
	key := strings.Index(content, `"action"`)
	if base < 0 || key < 0 {
		return 0, false
	}
	open := strings.IndexByte(content[key+len(`"action"`):], '"')
	if open < 0 {
		return 0, false
	}
	start := base + key + len(`"action"`) + open + 1

	offset := 0
	for _, t := range tokens {
		end := offset + len(t.Token)
		if end <= start {
			offset = end
			continue
		}
		// This token starts the action, after text every alternative shares.
		shared := b.String()[offset:start]
		mass := map[string]float64{}
		var total float64
		for _, alt := range t.TopLogprobs {
			rest, ok := strings.CutPrefix(alt.Token, shared)
			if !ok || rest == "" {
				continue
			}
			var begins []string
			for _, a := range Actions {
				if strings.HasPrefix(a, rest) || strings.HasPrefix(rest, a) {
					begins = append(begins, a)
				}
			}
			if len(begins) == 1 {
				p := math.Exp(alt.Logprob)
				mass[begins[0]] += p
				total += p
			}
		}
		if mass[action] == 0 {
			return 0, false
		}
		return mass[action] / total, true
	}
	return 0, false
}

// harmlessSection is the knowledge's heading for lines that never show a
// fault; each quoted text under it marks a line as harmless.
const harmlessSection = "## Harmless lines"

var quoted = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// harmless returns the quoted texts in the knowledge's harmless section.
func harmless(knowledge string) []string {
	_, section, ok := strings.Cut(knowledge, "\n"+harmlessSection)
	if !ok {
		return nil
	}
	section, _, _ = strings.Cut(section, "\n## ")
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(section, -1) {
		if text := strings.ReplaceAll(m[1], `\"`, `"`); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// without returns the lines that contain none of the texts.
func without(lines, texts []string) []string {
	var out []string
	for _, l := range lines {
		if !slices.ContainsFunc(texts, func(t string) bool { return strings.Contains(l, t) }) {
			out = append(out, l)
		}
	}
	return out
}

// Triager triages each incident as it is recorded, one at a time, oldest
// first. An incident is served once triaged, so serving them in order means a
// poller asking for everything after the newest ID it has seen misses none.
type Triager struct {
	Assistant *Assistant
	Store     *Store

	now                func() time.Time
	memoryAvailableMiB func() uint64
}

// Run triages the incidents waiting, then each one recorded, until ctx is
// done. Incidents left from an earlier run, such as a crash, are triaged
// first.
func (t *Triager) Run(ctx context.Context) {
	if t.now == nil {
		t.now = time.Now
	}
	if t.memoryAvailableMiB == nil {
		t.memoryAvailableMiB = func() uint64 { return hostInfo("").MemoryAvailableMiB }
	}
	started := t.now()
	// While the model is being fetched, look again every minute.
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		t.triagePending(ctx, started)
		select {
		case <-ctx.Done():
			return
		case <-t.Store.Added():
		case <-ticker.C:
		}
	}
}

// triagePending triages the waiting incidents in order. It stops at the first
// that must wait for the model, so that none is served before an older one.
func (t *Triager) triagePending(ctx context.Context, started time.Time) {
	pending, err := t.Store.Pending()
	if err != nil {
		log.Error().Str("component", "assistant").Err(err).Msg("cannot read the incidents to triage")
		return
	}
	for _, inc := range pending {
		triage := t.triage(ctx, inc, started)
		if triage == nil {
			return
		}
		inc.Triage = triage
		if err := t.Store.Save(inc); err != nil {
			log.Error().Str("component", "assistant").Str("incident", inc.ID).Err(err).Msg("cannot save the triage")
			return
		}
		log.Info().Str("component", "assistant").Str("incident", inc.ID).Str("status", triage.Status).
			Str("action", triage.Action).Str("reason", triage.Reason).Msg("incident triaged")
	}
}

// triage returns the outcome for one incident, or nil if it must wait: for
// the model, or for the next start when KubeSolo is shutting down.
func (t *Triager) triage(ctx context.Context, inc Incident, started time.Time) *Triage {
	if mem := t.memoryAvailableMiB(); mem < TriageMinMemoryMiB {
		return &Triage{Status: TriageSkipped, Time: t.now().UTC(),
			Reason: fmt.Sprintf("only %d MiB of memory was available; triage needs %d MiB", mem, TriageMinMemoryMiB)}
	}

	log.Info().Str("component", "assistant").Str("incident", inc.ID).Msg("triaging the incident...")
	tctx, cancel := context.WithTimeout(ctx, triageTimeout)
	defer cancel()
	d, err := t.Assistant.Diagnose(tctx, inc)
	now := t.now().UTC()
	switch {
	case err == nil:
		return &Triage{Status: TriageDone, Time: now, Diagnosis: d}
	case ctx.Err() != nil:
		return nil
	case errors.Is(err, ErrNotReady):
		if now.Sub(started) < ModelWait {
			return nil
		}
		return &Triage{Status: TriageSkipped, Time: now, Reason: fmt.Sprintf("%v; waited %s", err, ModelWait)}
	case errors.Is(err, ErrUnavailable):
		return &Triage{Status: TriageSkipped, Time: now, Reason: err.Error()}
	}
	return &Triage{Status: TriageFailed, Time: now, Reason: err.Error()}
}
