package upgrade

import (
	"os"
	"testing"
)

func TestKnownGoodConfig(t *testing.T) {
	l := testLayout(t)

	if raw, err := LoadKnownGoodConfig(l); err != nil || raw != nil {
		t.Fatalf("before any is recorded: %q, %v", raw, err)
	}

	first := []byte("network:\n  mtu: 1400\n")
	if wrote, err := SaveKnownGoodConfig(l, first); err != nil || !wrote {
		t.Fatalf("first save: wrote %v, %v", wrote, err)
	}
	if raw, _ := LoadKnownGoodConfig(l); string(raw) != string(first) {
		t.Errorf("loaded %q", raw)
	}
	// It holds the edge key, so it is as private as the configuration file.
	if fi, err := os.Stat(l.KnownGoodConfig()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v", fi.Mode(), err)
	}

	if wrote, err := SaveKnownGoodConfig(l, first); err != nil || wrote {
		t.Errorf("saving the same configuration again: wrote %v, %v", wrote, err)
	}

	second := []byte("network:\n  mtu: 1500\n")
	if wrote, err := SaveKnownGoodConfig(l, second); err != nil || !wrote {
		t.Fatalf("second save: wrote %v, %v", wrote, err)
	}
	if raw, _ := LoadKnownGoodConfig(l); string(raw) != string(second) {
		t.Errorf("loaded %q after the second save", raw)
	}
}
