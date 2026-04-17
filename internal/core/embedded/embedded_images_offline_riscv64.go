//go:build offline && riscv64

package embedded

import _ "embed"

// portainerAgentImageFile is empty for riscv64 as portainer-agent is not available for this architecture.
var portainerAgentImageFile []byte

//go:embed bin/images/local-path-provisioner.tar.gz
var localPathProvisionerImageFile []byte
