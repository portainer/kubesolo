package configapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// start brings the service up on a socket in a temp directory and returns the
// socket path plus a client that speaks to it.
func start(t *testing.T, socketPath string) (*Service, *http.Client) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, cancel, make(chan struct{}), Options{SocketPath: socketPath})

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run() }()

	select {
	case <-svc.readyCh:
	case err := <-errCh:
		cancel()
		t.Fatalf("service failed to start: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("service did not become ready")
	}

	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("service did not shut down")
		}
	})

	return svc, unixClient(socketPath)
}

func unixClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}
}

func TestServesOverTheSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "config.sock")
	_, client := start(t, socket)

	resp, err := client.Get("http://localhost/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestSocketIsOwnerOnly is the whole authorisation model: anyone who can open
// the socket can change KubeSolo's configuration.
func TestSocketIsOwnerOnly(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "config.sock")
	start(t, socket)

	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != socketMode {
		t.Errorf("socket mode = %04o, want %04o", got, socketMode)
	}
}

// TestSocketModeIgnoresUmask covers the window between Listen and Chmod: the
// socket is created 0777 minus umask, so a permissive umask would briefly leave
// it open to anyone.
func TestSocketModeIgnoresUmask(t *testing.T) {
	for _, umask := range []int{0o022, 0o077, 0o000} {
		t.Run(maskName(umask), func(t *testing.T) {
			dir := shortTempDir(t)
			old := setUmask(umask)
			t.Cleanup(func() { setUmask(old) })

			socket := filepath.Join(dir, "config.sock")
			start(t, socket)

			info, err := os.Stat(socket)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != socketMode {
				t.Errorf("socket mode = %04o under umask %04o, want %04o", got, umask, socketMode)
			}
		})
	}
}

// TestSocketRemovedOnShutdown keeps the next start clean rather than a reclaim.
func TestSocketRemovedOnShutdown(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "config.sock")

	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, cancel, make(chan struct{}), Options{SocketPath: socket})

	done := make(chan error, 1)
	go func() { done <- svc.Run() }()
	<-svc.readyCh

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Errorf("socket still present after shutdown: %v", err)
	}
}

// TestStaleSocketIsReclaimed covers a process killed without a chance to clean
// up: the socket file remains but nothing is listening.
func TestStaleSocketIsReclaimed(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "config.sock")

	// A socket file with no listener, exactly as SIGKILL would leave.
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	// Close removes the socket, so recreate the leftover deliberately.
	if l2, err := net.Listen("unix", socket); err == nil {
		if f, err := l2.(*net.UnixListener).File(); err == nil {
			_ = f.Close()
		}
		l2.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = l2.Close()
	}
	if _, err := os.Stat(socket); err != nil {
		t.Skipf("could not stage a stale socket on this platform: %v", err)
	}

	_, client := start(t, socket)

	resp, err := client.Get("http://localhost/healthz")
	if err != nil {
		t.Fatalf("service should have reclaimed the stale socket: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
}

// TestLiveSocketIsRefused is the counterpart. Removing the path unconditionally
// would let a second KubeSolo steal the socket from a running first one, which
// would then be serving a socket nothing can reach by name.
func TestLiveSocketIsRefused(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "config.sock")
	start(t, socket) // first instance keeps it

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := NewService(ctx, cancel, make(chan struct{}), Options{SocketPath: socket})

	err := second.Run()
	if err == nil {
		t.Fatal("a second instance must not take over a live socket")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error should say the socket is in use, got: %v", err)
	}
}

// TestNonSocketPathIsRefused guards against pointing the API at a real file and
// having it deleted.
func TestNonSocketPathIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(path, []byte("important"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := NewService(ctx, cancel, make(chan struct{}), Options{SocketPath: path})

	if err := svc.Run(); err == nil {
		t.Fatal("expected a refusal")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the existing file must not be removed: %v", err)
	}
}

func maskName(umask int) string { return fmt.Sprintf("umask_%04o", umask) }

// shortTempDir returns a temp directory with a minimal name. t.TempDir() embeds
// the test and subtest name, which for a table-driven test easily pushes the
// socket path past the 104-byte limit macOS imposes on sun_path.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
