//go:build external_deps

package embedded

// External-dependency builds use artifacts supplied by the host.
var (
	containerdShimBinary          []byte
	crunBinary                    []byte
	cniPluginBridge               []byte
	cniPluginHostLocal            []byte
	cniPluginPortmap              []byte
	cniPluginLoopback             []byte
	corednsImageFile              []byte
	sandboxImageFile              []byte
	portainerEdgeImageFile        []byte
	localPathProvisionerImageFile []byte
	d2kImageFile                  []byte
)
