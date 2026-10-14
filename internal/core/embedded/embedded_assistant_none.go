//go:build external_deps || !linux || !(amd64 || arm64)

package embedded

var llamaServerBinary []byte
