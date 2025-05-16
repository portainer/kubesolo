package filesystem

import (
	"fmt"
	"os"
)

// ExtractBinary extracts and installs a binary
// TODO: get the SHA of each binary and compare it to the expected SHA
func ExtractBinary(binary []byte, destFile string) error {
	if _, err := os.Stat(destFile); err == nil {
		return nil
	}

	if err := os.WriteFile(destFile, binary, 0755); err != nil {
		return fmt.Errorf("failed to write %s binary: %v", destFile, err)
	}

	return nil
}
