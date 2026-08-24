//go:build !external_deps && !offline

package embedded

// portainerEdgeImageFile is not embedded in the online build variant.
// The image will be pulled from the registry at runtime.
var portainerEdgeImageFile []byte

// localPathProvisionerImageFile is not embedded in the online build variant.
// The image will be pulled from the registry at runtime.
var localPathProvisionerImageFile []byte

// d2kImageFile is not embedded in the online build variant.
// The image will be pulled from the registry at runtime when --d2k is set.
var d2kImageFile []byte
