package executor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
)

// A signal while a version is being verified, such as the host shutting down,
// is not a verdict on that version, and the gate must not report it as one.
func TestGateReportsAnInterruptionAsSuch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	e := &executor{ctx: ctx, l: upgrade.NewLayout(dir, filepath.Join(dir, "config.yaml")), svc: &upgrade.Service{}}

	if got := e.gate("v1.2.2", time.Minute, false); got.kind != gateInterrupted {
		t.Errorf("gate after cancellation = %v (%s), want gateInterrupted", got.kind, got.reason)
	}
}
