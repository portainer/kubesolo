package kine

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/k3s-io/kine/pkg/app"
)

// unsetKineEnv unsets the environment variables kine's CLI flags read, so
// app.Config returns kine's defaults. An empty value still counts as set.
func unsetKineEnv(t *testing.T) {
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "KINE_") || strings.HasPrefix(name, "AWS_") {
			t.Setenv(name, value) // restores the variable after the test
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestGeneratedConfigKeepsKineDefaults(t *testing.T) {
	unsetKineEnv(t)
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

func TestGeneratedConfigOnlyOverridesExpectedFields(t *testing.T) {
	unsetKineEnv(t)
	s := &service{databaseDir: t.TempDir(), listener: "127.0.0.1:2379"}
	config := s.generateKineConfig()

	// kine only honours the apiserver's compaction requests when its own
	// compactor is off.
	if config.CompactInterval != 0 {
		t.Errorf("CompactInterval = %v, want 0", config.CompactInterval)
	}
	if config.CompactTimeout != 60*time.Second {
		t.Errorf("CompactTimeout = %v, want 60s", config.CompactTimeout)
	}

	if config.ConnectionPoolConfig.MaxOpen != 5 {
		t.Errorf("ConnectionPoolConfig.MaxOpen = %d, want 5", config.ConnectionPoolConfig.MaxOpen)
	}

	// Every other field must be kine's default.
	want := app.Config(nil)
	config.Endpoint, config.Listener, config.CompactTimeout = want.Endpoint, want.Listener, want.CompactTimeout
	config.ConnectionPoolConfig.MaxOpen = want.ConnectionPoolConfig.MaxOpen
	if !reflect.DeepEqual(config, want) {
		t.Errorf("config differs from kine's defaults:\n got  %+v\n want %+v", config, want)
	}
}
