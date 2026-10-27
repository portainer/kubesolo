package assistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// procNet writes /proc/net/tcp and tcp6 with the given lines after the header.
func procNet(t *testing.T, tcp, tcp6 string) string {
	t.Helper()
	dir := t.TempDir()
	const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	for name, lines := range map[string]string{"tcp": tcp, "tcp6": tcp6} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(header+lines), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestListeningPorts(t *testing.T) {
	dir := procNet(t,
		// 127.0.0.1:10248 listening; 127.0.0.1:2379 connected only.
		"   0: 0100007F:2808 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 101 1 0 100 0 0 10 0\n"+
			"   1: 0100007F:094B 0100007F:C350 01 00000000:00000000 00:00000000 00000000     0        0 102 1 0 20 4 30 10 -1\n",
		// [::]:6443 listening.
		"   0: 00000000000000000000000000000000:192B 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 103 1 0 100 0 0 10 0\n")
	got := listeningPorts(dir)
	if !got[10248] || !got[6443] || got[2379] {
		t.Errorf("listening ports = %v, want 10248 and 6443 only", got)
	}
	if listeningPorts(t.TempDir()) != nil {
		t.Error("without /proc/net, listening ports are unknown, not none")
	}
}

// A component that refuses its probe while its port listens is running:
// something in between, such as a firewall rule, blocks the probe.
func TestASnapshotSaysWhetherAFailingComponentListens(t *testing.T) {
	c := &Collector{
		Probes: map[string]func(context.Context) error{
			"kubelet": func(context.Context) error {
				return errors.New(`Get "http://127.0.0.1:10248/healthz": dial tcp 127.0.0.1:10248: connect: connection refused`)
			},
			"kine": func(context.Context) error {
				return errors.New("dial tcp 127.0.0.1:2379: connect: connection refused")
			},
			"disk":      func(context.Context) error { return errors.New("95% used") },
			"apiserver": func(context.Context) error { return nil },
		},
		Node:    func(context.Context) (string, error) { return "Ready", nil },
		DataDir: t.TempDir(),
		ProcNet: procNet(t, "   0: 0100007F:2808 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 101 1 0 100 0 0 10 0\n", ""),
	}
	s := c.Collect(context.Background())
	if len(s.Listening) != 2 || !s.Listening["kubelet"] || s.Listening["kine"] {
		t.Errorf("listening = %v, want kubelet true and kine false only", s.Listening)
	}
	if s.Node != "Ready" {
		t.Errorf("node = %q, want Ready", s.Node)
	}
}

// Without the API server, the node's readiness is unknown and left out.
func TestASnapshotWithoutTheNode(t *testing.T) {
	c := &Collector{
		Node:    func(context.Context) (string, error) { return "", errors.New("connection refused") },
		DataDir: t.TempDir(),
	}
	if s := c.Collect(context.Background()); s.Node != "" || s.Listening != nil {
		t.Errorf("node = %q, listening = %v, want neither", s.Node, s.Listening)
	}
}
