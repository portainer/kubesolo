//go:build !external_deps && offline && !amd64 && !arm64

package embedded

// d2kImageFile is empty for offline builds on architectures where
// portainer/d2k is not published (currently arm and riscv64). Operators
// running on these architectures cannot enable --d2k.
var d2kImageFile []byte
