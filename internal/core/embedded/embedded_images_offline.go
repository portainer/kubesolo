//go:build offline && !riscv64

package embedded

import _ "embed"

//go:embed bin/images/portainer-agent.tar.gz
var portainerEdgeImageFile []byte

//go:embed bin/images/local-path-provisioner.tar.gz
var localPathProvisionerImageFile []byte

// d2kImageFile is defined in arch-specific files. portainer/d2k publishes
// images for amd64 and arm64 only, so the offline embed is split out.
