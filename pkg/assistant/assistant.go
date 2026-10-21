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

// ErrBusy is returned while another question is being answered or an
// incident triaged. Running two at once would hold two copies of the model in
// memory.
var ErrBusy = errors.New("the assistant is busy answering a question or triaging an incident, try again shortly")

// ErrNotReady means the model or the knowledge is still being fetched;
// ErrUnavailable that it cannot be used.
var (
	ErrNotReady    = errors.New("still being downloaded, try again shortly")
	ErrUnavailable = errors.New("not available")
)

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

	// Knowledge is the Markdown file the model is given to triage incidents.
	// It is only ever read. Empty means it is still being fetched;
	// SetKnowledge supplies it.
	Knowledge string

	// ContextSize is the context window in tokens.
	ContextSize int

	// StartTimeout bounds how long the model may take to load.
	StartTimeout time.Duration

	// ShaderCacheDir keeps the shaders a GPU build compiles, which would
	// otherwise be compiled again in every llama-server, for the length of a
	// long prompt; empty leaves the drivers' own default.
	ShaderCacheDir string
}

// Assistant answers questions with the model. It is safe for concurrent use,
// and answers one question at a time.
type Assistant struct {
	cfg Config
	mu  sync.Mutex

	filesMu   sync.Mutex
	model     file
	knowledge file
}

// file is a file the assistant reads, which may still be being fetched.
type file struct {
	path string
	err  error
}

// get returns the file's path, or why it cannot be read yet.
func (f file) get(what string) (string, error) {
	switch {
	case f.err != nil:
		return "", fmt.Errorf("the %s is %w: %w", what, ErrUnavailable, f.err)
	case f.path == "":
		return "", fmt.Errorf("the %s is %w", what, ErrNotReady)
	}
	if _, err := os.Stat(f.path); err != nil {
		return "", fmt.Errorf("the %s is %w: %w", what, ErrUnavailable, err)
	}
	return f.path, nil
}

// New returns an assistant. It starts nothing until asked.
func New(cfg Config) *Assistant {
	return &Assistant{cfg: cfg, model: file{path: cfg.Model}, knowledge: file{path: cfg.Knowledge}}
}

// SetModel supplies the model once it has been fetched, or why it could not be.
func (a *Assistant) SetModel(path string, err error) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	a.model = file{path, err}
}

// SetKnowledge supplies the knowledge once it has been fetched, or why it
// could not be.
func (a *Assistant) SetKnowledge(path string, err error) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	a.knowledge = file{path, err}
}

// ModelFile returns the model's path, or "" until it is fetched.
func (a *Assistant) ModelFile() string {
	m, _ := a.files()
	return m.path
}

func (a *Assistant) files() (model, knowledge file) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	return a.model, a.knowledge
}

// Ask starts llama-server, asks it the question, and stops it again, whether
// or not it answered. It returns ErrBusy if another question is in progress.
func (a *Assistant) Ask(ctx context.Context, question string) (string, error) {
	if !a.mu.TryLock() {
		return "", ErrBusy
	}
	defer a.mu.Unlock()

	m, _ := a.files()
	model, err := m.get("model")
	if err != nil {
		return "", err
	}
	ans, err := a.run(ctx, model, chatRequest{Messages: []message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: question},
	}})
	return ans.content, err
}

// run starts llama-server with the model, sends it the request, and stops it
// again, whether or not it answered. The caller holds a.mu.
func (a *Assistant) run(ctx context.Context, model string, req chatRequest) (answer, error) {
	port, err := freePort()
	if err != nil {
		return answer{}, fmt.Errorf("find a port for llama-server: %w", err)
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
	if dir := a.cfg.ShaderCacheDir; dir != "" {
		// A service often has no home directory for the drivers' defaults.
		if err := os.MkdirAll(dir, 0o700); err == nil {
			cmd.Env = append(os.Environ(),
				"__GL_SHADER_DISK_CACHE=1", "__GL_SHADER_DISK_CACHE_PATH="+dir, "__GL_SHADER_DISK_CACHE_SKIP_CLEANUP=1",
				"MESA_SHADER_CACHE_DIR="+dir)
		}
	}
	output := &logWriter{}
	cmd.Stdout = output
	cmd.Stderr = output
	setProcAttr(cmd)

	log.Info().Str("component", "assistant").Str("model", model).Msg("starting llama-server...")
	if err := cmd.Start(); err != nil {
		return answer{}, fmt.Errorf("start llama-server: %w", err)
	}
	proc := &process{cmd: cmd, exited: make(chan struct{})}
	go func() {
		proc.err = cmd.Wait()
		close(proc.exited)
	}()
	defer proc.stop()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := waitReady(ctx, base, proc, a.cfg.StartTimeout); err != nil {
		return answer{}, err
	}
	return complete(ctx, base, req)
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

type chatRequest struct {
	Messages []message `json:"messages"`

	// ResponseFormat, when set, constrains the answer to a JSON schema.
	ResponseFormat any `json:"response_format,omitempty"`

	// MaxTokens bounds the answer; 0 leaves it to the context size.
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature, when set, replaces llama-server's sampling temperature.
	Temperature *float64 `json:"temperature,omitempty"`

	// Logprobs asks for each token's log probability, and TopLogprobs for
	// that many alternatives the model weighed at each.
	Logprobs    bool `json:"logprobs,omitempty"`
	TopLogprobs int  `json:"top_logprobs,omitempty"`
}

// answer is what the model wrote, and the probabilities of its tokens when
// they were asked for.
type answer struct {
	content string
	tokens  []tokenLogprob
}

// tokenLogprob is a token of an answer with its log probability and, when
// asked for, the alternatives the model weighed. llama-server reports them
// before a response format constrains the choice, so they are the model's own
// beliefs.
type tokenLogprob struct {
	Token       string         `json:"token"`
	Logprob     float64        `json:"logprob"`
	TopLogprobs []tokenLogprob `json:"top_logprobs,omitempty"`
}

func complete(ctx context.Context, base string, req chatRequest) (answer, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return answer{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return answer{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return answer{}, fmt.Errorf("ask llama-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return answer{}, fmt.Errorf("llama-server answered %s: %s", resp.Status, bytes.TrimSpace(msg))
	}

	var out struct {
		Choices []struct {
			Message  message `json:"message"`
			Logprobs struct {
				Content []tokenLogprob `json:"content"`
			} `json:"logprobs"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return answer{}, fmt.Errorf("decode llama-server answer: %w", err)
	}
	if len(out.Choices) == 0 {
		return answer{}, errors.New("llama-server returned no answer")
	}
	return answer{content: out.Choices[0].Message.Content, tokens: out.Choices[0].Logprobs.Content}, nil
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
