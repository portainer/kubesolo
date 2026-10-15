package cli

import (
	"testing"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
)

// --health-timeout=0 is valid and means the default, as for a host upgrade.
func TestHealthTimeout(t *testing.T) {
	if got := healthTimeout(0); got != upgrade.DefaultHealthTimeout {
		t.Errorf("0 = %s, want the default %s", got, upgrade.DefaultHealthTimeout)
	}
	if got := healthTimeout(3 * time.Minute); got != 3*time.Minute {
		t.Errorf("3m = %s", got)
	}
}
