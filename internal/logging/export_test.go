package logging

import "io"

// SetBridgeOutput points the bridge logger at w, for tests that read what
// Kubernetes and containerd log lines turn into.
func SetBridgeOutput(w io.Writer) {
	setBridgeOutput(w)
}

var (
	KlogComponent   = klogComponent
	LogrusComponent = logrusComponent
	CallerName      = callerName
)

// ModuleRoot is the build directory prefix callerName replaces.
func ModuleRoot() string { return moduleRoot }
