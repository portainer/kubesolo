//go:build assistant_eval

package assistant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestEvalTriage runs the triage test set in test/assistant against a real
// llama-server and model, through Diagnose, and reports how well the model
// did. It needs a host that can run the model; see test/assistant/README.md.
func TestEvalTriage(t *testing.T) {
	server, model := os.Getenv("ASSISTANT_EVAL_LLAMA_SERVER"), os.Getenv("ASSISTANT_EVAL_MODEL")
	if server == "" || model == "" {
		t.Skip("set ASSISTANT_EVAL_LLAMA_SERVER and ASSISTANT_EVAL_MODEL")
	}
	knowledge := envOr("ASSISTANT_EVAL_KNOWLEDGE", "../../build/assistant/knowledge.md")
	cases := envOr("ASSISTANT_EVAL_CASES", "../../test/assistant/cases")

	a := New(Config{Binary: server, Model: model, Knowledge: knowledge, ContextSize: 8192, StartTimeout: 5 * time.Minute})
	var all []outcome
	for _, split := range []string{"dev", "heldout"} {
		files, err := filepath.Glob(filepath.Join(cases, split, "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no cases in %s: %v", filepath.Join(cases, split), err)
		}
		var right, named, cut int
		var took time.Duration
		for _, f := range files {
			c := readCase(t, f)
			start := time.Now()
			d, err := a.Diagnose(context.Background(), c.Incident)
			elapsed := time.Since(start)
			took += elapsed
			name := strings.TrimSuffix(filepath.Base(f), ".json")
			if err != nil {
				t.Logf("%-8s %-30s ERROR %v", split, name, err)
				continue
			}
			ok := slices.Contains(c.Expect.Actions, d.Action)
			all = append(all, outcome{split: split, action: d.Action, right: ok, probability: d.ActionProbability})
			mentioned := mentions(d, c.Expect.Mentions)
			truncated := !strings.HasSuffix(strings.TrimSpace(d.Summary), ".")
			right, named, cut = right+b2i(ok), named+b2i(mentioned), cut+b2i(truncated)
			t.Logf("%-8s %-30s %-5s %-26s p=%.2f %-6s named=%-5v cut=%-5v %3.0fs  %s",
				split, name, mark(ok), d.Action, d.ActionProbability, d.Rating, mentioned, truncated, elapsed.Seconds(), d.Summary)
		}
		t.Logf("%s: actions %d/%d, named %d/%d, summaries cut %d, %.0fs a case",
			split, right, len(files), named, len(files), cut, took.Seconds()/float64(len(files)))
	}
	logThresholds(t, all)
}

// outcome is one case's result, for weighing thresholds.
type outcome struct {
	split       string
	action      string
	right       bool
	probability float64
}

// logThresholds shows, for each threshold on the action's probability, what
// acting only above it would do: a right action kept, a wrong one let through
// (a false action), or either one turned into report_only. report_only is
// never a false action: at worst it misses a fix.
func logThresholds(t *testing.T, all []outcome) {
	t.Logf("threshold  acted right  false actions  (dev / heldout)")
	for _, threshold := range []float64{0, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95} {
		count := func(split string) (right, wrong int) {
			for _, o := range all {
				if o.split != split || o.action == "report_only" || o.probability < threshold {
					continue
				}
				if o.right {
					right++
				} else {
					wrong++
				}
			}
			return right, wrong
		}
		dr, dw := count("dev")
		hr, hw := count("heldout")
		t.Logf("%9.2f  %5d / %-5d  %6d / %-6d", threshold, dr, hr, dw, hw)
	}
}

type evalCase struct {
	Incident Incident `json:"incident"`
	Expect   struct {
		// Actions are the acceptable actions.
		Actions []string `json:"actions"`
		// Mentions are words of which the summary or cause should name one.
		Mentions []string `json:"mentions"`
	} `json:"expect"`
}

func readCase(t *testing.T, path string) evalCase {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c evalCase
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return c
}

func mentions(d Diagnosis, words []string) bool {
	if len(words) == 0 {
		return true
	}
	text := strings.ToLower(d.Summary + " " + d.Cause)
	return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(text, strings.ToLower(w)) })
}

func mark(ok bool) string {
	if ok {
		return "ok"
	}
	return "WRONG"
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
