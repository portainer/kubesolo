package service

import (
	"strings"
	"testing"
)

func TestLockInstance(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")

	unlock, err := LockInstance("dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockInstance("dev"); err == nil || !strings.Contains(err.Error(), "kubesolo-dev") {
		t.Errorf("second lock of the same instance: err = %v", err)
	}
	other, err := LockInstance("prod")
	if err != nil {
		t.Errorf("another instance is not locked by the first: %v", err)
	} else {
		other()
	}
	unlock()
	again, err := LockInstance("dev")
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	again()
}
