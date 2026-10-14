// Package assistant runs KubeSolo's own diagnostic model.
//
// The model is served by llama-server, started as a child process of KubeSolo
// rather than as a pod, so that the assistant still answers when containerd,
// kine or the API server is broken. It runs only while answering a question:
// every Ask starts the server, waits for the model to load, asks, and stops it
// again, so that nothing holds the model's memory in between.
package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// ErrBusy is returned while another question is being answered. Answering two
// at once would hold two copies of the model in memory.
var ErrBusy = errors.New("the assistant is already answering a question")

// systemPrompt tells the model what it is talking about.
const systemPrompt = "You are the KubeSolo assistant. KubeSolo is a single-node Kubernetes distribution for edge devices. Answer briefly and plainly."

const (
	healthPollInterval = 250 * time.Millisecond

	// stopGrace is how long llama-server has to exit on SIGTERM before it is killed.
	stopGrace = 5 * time.Second
)

// Config describes how to run llama-server.
type Config struct {
	// Binary is the llama-server executable.
	Binary string

	// Model is the GGUF model file. It is only ever read. Empty means it is
	// still being fetched; SetModel supplies it.
	Model string

	// ContextSize is the context window in tokens.
	ContextSize int

	// StartTimeout bounds how long the model may take to load.
	StartTimeout time.Duration
}

// Assistant answers questions with the model. It is safe for concurrent use,
// and answers one question at a time.
type Assistant struct {
	cfg Config
	mu  sync.Mutex

	modelMu  sync.Mutex
	model    string
	modelErr error
}

// New returns an assistant. It starts nothing until asked.
func New(cfg Config) *Assistant {
	return &Assistant{cfg: cfg, model: cfg.Model}
}

// SetModel supplies the model once it has been fetched, or why it could not be.
func (a *Assistant) SetModel(path string, err error) {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	a.model, a.modelErr = path, err
}

// Ask starts llama-server, asks it the question, and stops it again, whether
// or not it answered. It returns ErrBusy if another question is in progress.
func (a *Assistant) Ask(ctx context.Context, question string) (string, error) {
	if !a.mu.TryLock() {
		return "", ErrBusy
	}
	defer a.mu.Unlock()

	a.modelMu.Lock()
	model, modelErr := a.model, a.modelErr
	a.modelMu.Unlock()
	switch {
	case modelErr != nil:
		return "", fmt.Errorf("the model is not available: %w", modelErr)
	case model == "":
		return "", errors.New("the model is still being downloaded, try again shortly")
	}
	if _, err := os.Stat(model); err != nil {
		return "", fmt.Errorf("model %s cannot be read: %w", model, err)
	}

	port, err := freePort()
	if err != nil {
		return "", fmt.Errorf("find a port for llama-server: %w", err)
	}

	cmd := exec.Command(a.cfg.Binary,
		"--model", model,
		"--ctx-size", strconv.Itoa(a.cfg.ContextSize),
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--no-webui",
		// Reasoning models such as Qwen3.5 otherwise think at length first,
		// which on a CPU costs minutes.
		"--reasoning-budget", "0",
	)
	output := &logWriter{}
	cmd.Stdout = output
	cmd.Stderr = output
	setProcAttr(cmd)

	log.Info().Str("component", "assistant").Str("model", model).Msg("starting llama-server...")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start llama-server: %w", err)
	}
	proc := &process{cmd: cmd, exited: make(chan struct{})}
	go func() {
		proc.err = cmd.Wait()
		close(proc.exited)
	}()
	defer proc.stop()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := waitReady(ctx, base, proc, a.cfg.StartTimeout); err != nil {
		return "", err
	}
	return complete(ctx, base, question)
}

// freePort asks the kernel for an unused loopback port. Another process could
// take it before llama-server binds it; llama-server then exits, and Ask
// reports that.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitReady polls /health, which answers 503 while the model loads.
func waitReady(ctx context.Context, base string, proc *process, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(healthPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-proc.exited:
			return fmt.Errorf("llama-server exited before the model loaded: %v", proc.err)
		case <-ctx.Done():
			return fmt.Errorf("llama-server was not ready within %s: %w", timeout, ctx.Err())
		case <-ticker.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func complete(ctx context.Context, base, question string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"messages": []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: question},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ask llama-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("llama-server answered %s: %s", resp.Status, bytes.TrimSpace(msg))
	}

	var out struct {
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode llama-server answer: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("llama-server returned no answer")
	}
	return out.Choices[0].Message.Content, nil
}

// process is a running llama-server. exited is closed once it has been
// reaped, after which err holds how it ended.
type process struct {
	cmd    *exec.Cmd
	exited chan struct{}
	err    error
}

// stop ends llama-server and waits for it, so that its memory is free when
// Ask returns.
func (p *process) stop() {
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.exited:
	case <-time.After(stopGrace):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
	log.Info().Str("component", "assistant").Msg("llama-server stopped")
}

// logWriter passes llama-server's output to the debug log a line at a time.
type logWriter struct {
	buf []byte
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		log.Debug().Str("component", "assistant").Msg(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}
