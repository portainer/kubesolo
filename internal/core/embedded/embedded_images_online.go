//go:build !offline

package embedded

// portainerAgentImageFile is not embedded in the online build variant.
// The image will be pulled from the registry at runtime.
var portainerAgentImageFile []byte

// localPathProvisionerImageFile is not embedded in the online build variant.
// The image will be pulled from the registry at runtime.
var localPathProvisionerImageFile []byte
