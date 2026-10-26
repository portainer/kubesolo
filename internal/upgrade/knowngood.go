package upgrade

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
)

// SaveKnownGoodConfig records raw, the configuration file KubeSolo started
// with, as known to be good. It is written only when it differs from the one
// recorded, and readable by root only: it holds the edge key.
func SaveKnownGoodConfig(l Layout, raw []byte) (bool, error) {
	current, err := LoadKnownGoodConfig(l)
	if err != nil {
		return false, err
	}
	if current != nil && bytes.Equal(current, raw) {
		return false, nil
	}
	return true, WriteFileAtomic(l.KnownGoodConfig(), raw, 0o600)
}

// LoadKnownGoodConfig returns the known-good configuration, or nil when none
// has been recorded.
func LoadKnownGoodConfig(l Layout) ([]byte, error) {
	raw, err := os.ReadFile(l.KnownGoodConfig())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}
