//go:build offline && riscv64

package embedded

import _ "embed"

// portainerEdgeImageFile is empty for riscv64 as portainer-agent is not available for this architecture.
var portainerEdgeImageFile []byte

//go:embed bin/images/local-path-provisioner.tar.gz
var localPathProvisionerImageFile []byte

// d2kImageFile is declared in embedded_d2k_offline_unsupported.go for arches
// where portainer/d2k is not published (offline && !amd64 && !arm64).
