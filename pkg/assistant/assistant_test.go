package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// The test binary doubles as a fake llama-server: run with FAKE_LLAMA_MODE set,
// it serves the two endpoints the assistant uses instead of running the tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_LLAMA_MODE"); mode != "" {
		fakeLlamaServer(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeLlamaServer(mode string) {
	fs := flag.NewFlagSet("llama-server", flag.ContinueOnError)
	port := fs.Int("port", 0, "")
	fs.String("model", "", "")
	fs.String("host", "", "")
	fs.String("ctx-size", "", "")
	fs.String("reasoning-budget", "", "")
	fs.Bool("no-webui", false, "")
	_ = fs.Parse(os.Args[1:])

	if f := os.Getenv("FAKE_LLAMA_RECORD"); f != "" {
		_ = os.WriteFile(f, []byte(fmt.Sprintf("%d\n%s", os.Getpid(), strings.Join(os.Args[1:], " "))), 0o644)
	}
	if mode == "crash" {
		os.Exit(3)
	}

	var healthChecks atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		// Like the real server, answer 503 while the model loads.
		if mode == "never-ready" || healthChecks.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if f := os.Getenv("FAKE_LLAMA_REQUESTS"); f != "" {
			out, _ := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			_, _ = out.Write(append(body, '\n'))
			_ = out.Close()
		}
		var req struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.Unmarshal(body, &req)
		if mode == "slow" {
			time.Sleep(time.Second)
		}
		last := req.Messages[len(req.Messages)-1]
		content := "you asked: " + last.Content
		switch mode {
		case "diagnose":
			content = `{"summary":"kine is down","cause":"the database is locked","action":"repair_database"}`
		case "off-schema":
			content = `{"summary":"kine is down","cause":"unknown","action":"reboot_the_node"}`
		}
		choice := map[string]any{"message": map[string]string{"role": "assistant", "content": content}}
		if mode == "diagnose" {
			choice["logprobs"] = diagnoseLogprobs
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{choice}})
	})
	_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", *port), mux)
}

// diagnoseLogprobs are the token probabilities of the "diagnose" answer, as
// llama-server reports them: where the action starts, the model gave repair
// 0.6, restart 0.2 and report 0.1; "re" could begin three actions and "If"
// none.
var diagnoseLogprobs = map[string]any{"content": []any{
	map[string]any{"token": `{"summary":"kine is down","cause":"the database is locked","action":"`, "logprob": 0},
	map[string]any{"token": "repair", "logprob": math.Log(0.6), "top_logprobs": []any{
		map[string]any{"token": "repair", "logprob": math.Log(0.6)},
		map[string]any{"token": "restart", "logprob": math.Log(0.2)},
		map[string]any{"token": "report", "logprob": math.Log(0.1)},
		map[string]any{"token": "re", "logprob": math.Log(0.05)},
		map[string]any{"token": "If", "logprob": math.Log(0.05)},
	}},
	map[string]any{"token": "_database", "logprob": 0},
	map[string]any{"token": `"}`, "logprob": 0},
}}

// newTestAssistant runs this test binary as llama-server in the given mode, and
// returns the file it records its pid and arguments to.
func newTestAssistant(t *testing.T, mode string) (*Assistant, string) {
	t.Helper()
	record := filepath.Join(t.TempDir(), "record")
	t.Setenv("FAKE_LLAMA_MODE", mode)
	t.Setenv("FAKE_LLAMA_RECORD", record)

	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(model, []byte("GGUF"), 0o444); err != nil {
		t.Fatal(err)
	}
	return New(Config{
		Binary:       os.Args[0],
		Model:        model,
		ContextSize:  8192,
		StartTimeout: 3 * time.Second,
	}), record
}

func recorded(t *testing.T, record string) (pid int, args string) {
	t.Helper()
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("llama-server never started: %v", err)
	}
	pidLine, args, _ := strings.Cut(string(b), "\n")
	if _, err := fmt.Sscan(pidLine, &pid); err != nil {
		t.Fatal(err)
	}
	return pid, args
}

// assertGone fails unless the process has exited: the assistant must never
// leave a model in memory after answering.
func assertGone(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("llama-server (pid %d) is still running after Ask returned", pid)
	}
}

func TestAskAnswersAndStopsTheServer(t *testing.T) {
	a, record := newTestAssistant(t, "ok")

	answer, err := a.Ask(context.Background(), "why did containerd restart?")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "you asked: why did containerd restart?" {
		t.Errorf("answer = %q", answer)
	}

	pid, args := recorded(t, record)
	for _, want := range []string{
		"--model " + a.cfg.Model,
		"--ctx-size 8192",
		"--host 127.0.0.1",
		"--no-webui",
		// Qwen3.5 reasons at length before answering unless told not to.
		"--reasoning-budget 0",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("llama-server args %q lack %q", args, want)
		}
	}
	assertGone(t, pid)
}

func TestAskStopsAServerThatNeverBecomesReady(t *testing.T) {
	a, record := newTestAssistant(t, "never-ready")
	a.cfg.StartTimeout = time.Second

	_, err := a.Ask(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("expected a readiness error, got %v", err)
	}
	pid, _ := recorded(t, record)
	assertGone(t, pid)
}

func TestAskReportsAServerThatExits(t *testing.T) {
	a, _ := newTestAssistant(t, "crash")

	start := time.Now()
	_, err := a.Ask(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("expected an exit error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("a server that exits should be reported at once, not after the start timeout")
	}
}

func TestAskStopsTheServerWhenTheCallerGivesUp(t *testing.T) {
	a, record := newTestAssistant(t, "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := a.Ask(ctx, "hello"); err == nil {
		t.Fatal("expected the cancelled request to fail")
	}
	pid, _ := recorded(t, record)
	assertGone(t, pid)
}

// Two questions at once would hold two copies of the model in memory.
func TestAskAnswersOneQuestionAtATime(t *testing.T) {
	a, _ := newTestAssistant(t, "slow")

	first := make(chan error, 1)
	go func() {
		_, err := a.Ask(context.Background(), "first")
		first <- err
	}()
	time.Sleep(200 * time.Millisecond)

	if _, err := a.Ask(context.Background(), "second"); !errors.Is(err, ErrBusy) {
		t.Errorf("second question: got %v, want ErrBusy", err)
	}
	if err := <-first; err != nil {
		t.Errorf("first question: %v", err)
	}
}

func TestAskWithoutTheModelStartsNothing(t *testing.T) {
	a, record := newTestAssistant(t, "ok")
	a.SetModel("/nonexistent/model.gguf", nil)

	if _, err := a.Ask(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "/nonexistent/model.gguf") {
		t.Fatalf("expected an error naming the model, got %v", err)
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("llama-server was started without a model")
	}
}

func compress(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// Install replaces a binary left by an older KubeSolo, which a
// write-if-missing extraction would keep running after an upgrade.
func TestInstallReplacesAnOutdatedBinary(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "assistant", "llama-server")

	if err := Install(compress(t, []byte("v1")), dest); err != nil {
		t.Fatal(err)
	}
	if err := Install(compress(t, []byte("v2")), dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Errorf("binary = %q, want v2", got)
	}
	if info, _ := os.Stat(dest); info.Mode().Perm()&0o100 == 0 {
		t.Errorf("binary is not executable: %v", info.Mode())
	}
}

func TestInstallWithoutABinary(t *testing.T) {
	if err := Install(nil, filepath.Join(t.TempDir(), "llama-server")); err == nil {
		t.Error("expected an error when this build has no llama-server")
	}
}

// A model fetched from a registry is not there until the download finishes.
func TestAskBeforeTheModelIsReady(t *testing.T) {
	a, record := newTestAssistant(t, "ok")
	model := a.cfg.Model
	a = New(Config{Binary: a.cfg.Binary, ContextSize: 8192, StartTimeout: 3 * time.Second})

	if _, err := a.Ask(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "still being downloaded") {
		t.Fatalf("expected a not-ready error, got %v", err)
	}

	a.SetModel("", errors.New("registry unreachable"))
	if _, err := a.Ask(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "registry unreachable") {
		t.Fatalf("expected the fetch error, got %v", err)
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("llama-server was started without a model")
	}

	a.SetModel(model, nil)
	if _, err := a.Ask(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
}
