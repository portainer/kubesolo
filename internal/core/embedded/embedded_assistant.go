//go:build !external_deps && linux && (amd64 || arm64)

package embedded

import (
	_ "embed"
)

// llama-server is only built for the architectures the assistant supports.
//
//go:embed bin/llama-server.zst
var llamaServerBinary []byte
