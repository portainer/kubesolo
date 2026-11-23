package kine

import (
	"testing"
	"time"
)

func TestGeneratedConfigKeepsKineDefaults(t *testing.T) {
	s := &service{databaseDir: t.TempDir(), listener: "127.0.0.1:2379"}
	config := s.generateKineConfig()

	// PollBatchSize 0 removes the LIMIT from the poll query and stops the poll
	// loop from ever waiting, so it spins at full CPU.
	if config.PollBatchSize <= 0 {
		t.Errorf("PollBatchSize = %d, want > 0", config.PollBatchSize)
	}
	// CompactMinRetain 0 lets compaction run right up to the current revision.
	if config.CompactMinRetain <= 0 {
		t.Errorf("CompactMinRetain = %d, want > 0", config.CompactMinRetain)
	}
	// An empty version fails the apiserver's etcd version check, which turns
	// off RequestWatchProgress.
	if config.EmulatedETCDVersion == "" {
		t.Error("EmulatedETCDVersion is empty")
	}
}

func TestGeneratedConfigKeepsOverrides(t *testing.T) {
	s := &service{databaseDir: t.TempDir(), listener: "127.0.0.1:2379"}
	config := s.generateKineConfig()

	if config.Listener != s.listener {
		t.Errorf("Listener = %q, want %q", config.Listener, s.listener)
	}
	if pool := config.ConnectionPoolConfig; pool.MaxIdle != 3 || pool.MaxOpen != 5 || pool.MaxLifetime != 60*time.Second {
		t.Errorf("ConnectionPoolConfig = %+v, want MaxIdle 3, MaxOpen 5, MaxLifetime 60s", pool)
	}
	if config.NotifyInterval != 15*time.Second {
		t.Errorf("NotifyInterval = %v, want 15s", config.NotifyInterval)
	}
	if config.CompactInterval != 5*time.Minute {
		t.Errorf("CompactInterval = %v, want 5m", config.CompactInterval)
	}
	if config.CompactTimeout != 60*time.Second {
		t.Errorf("CompactTimeout = %v, want 60s", config.CompactTimeout)
	}
}
