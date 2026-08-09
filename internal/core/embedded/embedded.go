//go:build (linux && amd64) || (linux && arm64) || (linux && arm)

package embedded

import (
	_ "embed"
)

//go:embed bin/containerd/bin/containerd-shim-runc-v2.zst
var containerdShimBinary []byte

//go:embed bin/crun.zst
var crunBinary []byte

//go:embed bin/cni/bridge.zst
var cniPluginBridge []byte

//go:embed bin/cni/host-local.zst
var cniPluginHostLocal []byte

//go:embed bin/cni/portmap.zst
var cniPluginPortmap []byte

//go:embed bin/cni/loopback.zst
var cniPluginLoopback []byte

//go:embed bin/images/coredns.tar.gz
var corednsImageFile []byte

//go:embed bin/images/pause.tar.gz
var sandboxImageFile []byte
