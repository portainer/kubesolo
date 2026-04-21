//go:build offline && !riscv64

package embedded

import _ "embed"

//go:embed bin/images/portainer-agent.tar.gz
var portainerAgentImageFile []byte

//go:embed bin/images/local-path-provisioner.tar.gz
var localPathProvisionerImageFile []byte
