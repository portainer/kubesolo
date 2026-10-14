package assistant

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

// Install writes the embedded, zstd-compressed llama-server to dest, replacing
// whatever is there if it differs, so that an upgrade of KubeSolo also upgrades
// llama-server. The file is replaced by rename, never rewritten in place.
func Install(compressed []byte, dest string) error {
	if len(compressed) == 0 {
		return errors.New("this build of KubeSolo does not include llama-server")
	}
	decoder, err := zstd.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	defer decoder.Close()
	binary, err := io.ReadAll(decoder)
	if err != nil {
		return fmt.Errorf("decompress llama-server: %w", err)
	}

	if current, err := os.ReadFile(dest); err == nil && bytes.Equal(current, binary) {
		return nil
	}

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".llama-server-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}
