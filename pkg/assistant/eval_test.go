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
			mentioned := mentions(d, c.Expect.Mentions)
			truncated := !strings.HasSuffix(strings.TrimSpace(d.Summary), ".")
			right, named, cut = right+b2i(ok), named+b2i(mentioned), cut+b2i(truncated)
			t.Logf("%-8s %-30s %-5s %-26s %-6s named=%-5v cut=%-5v %3.0fs  %s",
				split, name, mark(ok), d.Action, d.Confidence, mentioned, truncated, elapsed.Seconds(), d.Summary)
		}
		t.Logf("%s: actions %d/%d, named %d/%d, summaries cut %d, %.0fs a case",
			split, right, len(files), named, len(files), cut, took.Seconds()/float64(len(files)))
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
