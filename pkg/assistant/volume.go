package assistant

import (
	"fmt"
	"path"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// VolumePrefix marks a model or knowledge kept in a PersistentVolumeClaim:
// pvc:<namespace>/<claim>/<path in the volume>.
const VolumePrefix = "pvc:"

// VolumeSource is a file in a PersistentVolumeClaim.
type VolumeSource struct {
	Namespace, Claim, Path string
}

// ParseVolumeSource reads pvc:<namespace>/<claim>/<path in the volume>. The
// path must stay inside the volume.
func ParseVolumeSource(s string) (VolumeSource, error) {
	rest, ok := strings.CutPrefix(s, VolumePrefix)
	if !ok {
		return VolumeSource{}, fmt.Errorf("%q does not start with %s", s, VolumePrefix)
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || parts[2] == "" {
		return VolumeSource{}, fmt.Errorf("%q must be %s<namespace>/<claim>/<path in the volume>", s, VolumePrefix)
	}
	v := VolumeSource{Namespace: parts[0], Claim: parts[1], Path: parts[2]}
	if errs := validation.IsDNS1123Label(v.Namespace); len(errs) > 0 {
		return VolumeSource{}, fmt.Errorf("%q: namespace %q: %s", s, v.Namespace, errs[0])
	}
	if errs := validation.IsDNS1123Subdomain(v.Claim); len(errs) > 0 {
		return VolumeSource{}, fmt.Errorf("%q: claim %q: %s", s, v.Claim, errs[0])
	}
	if path.Clean(v.Path) != v.Path || strings.HasPrefix(v.Path, "/") || v.Path == ".." || strings.HasPrefix(v.Path, "../") {
		return VolumeSource{}, fmt.Errorf("%q: the path in the volume must be relative and clean, and stay inside it", s)
	}
	return v, nil
}
