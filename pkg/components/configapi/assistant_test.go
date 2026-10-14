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
