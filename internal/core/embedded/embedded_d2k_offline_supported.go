//go:build !external_deps && offline && (amd64 || arm64)

package embedded

import _ "embed"

//go:embed bin/images/d2k.tar.gz
var d2kImageFile []byte
