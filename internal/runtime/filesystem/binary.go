package filesystem

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// ExtractBinary extracts a zstd-compressed binary and installs it to destFile.
// TODO: get the SHA of each binary and compare it to the expected SHA
func ExtractBinary(compressed []byte, destFile string) error {
	if _, err := os.Stat(destFile); err == nil {
		return nil
	}

	decoder, err := zstd.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("failed to create zstd decoder for %s: %v", destFile, err)
	}
	defer decoder.Close()

	decompressed, err := io.ReadAll(decoder)
	if err != nil {
		return fmt.Errorf("failed to decompress %s: %v", destFile, err)
	}

	if err := os.WriteFile(destFile, decompressed, 0755); err != nil {
		return fmt.Errorf("failed to write %s binary: %v", destFile, err)
	}

	return nil
}
