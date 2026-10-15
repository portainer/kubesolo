package service

import (
	"fmt"
	"path"
	"strings"
)

// ParseContainerMounts parses a comma-separated list of SRC:DST[:ro|rw] host
// directory mounts for the KubeSolo container — kind's extraMounts — into
// Docker binds. Both paths must be absolute. KubeSolo's data and configuration
// volumes and kernel modules cannot be replaced, but a directory inside the data volume can be
// mounted over, for example containerd's registry configuration.
func ParseContainerMounts(spec string) ([]string, error) {
	var binds []string
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("invalid container mount %q: expected SRC:DST or SRC:DST:ro", entry)
		}
		src, dst := parts[0], parts[1]
		if !path.IsAbs(src) || !path.IsAbs(dst) {
			return nil, fmt.Errorf("invalid container mount %q: both paths must be absolute", entry)
		}
		if d := path.Clean(dst); d == containerDataDir || d == containerConfigDir || d == "/lib/modules" {
			return nil, fmt.Errorf("invalid container mount %q: %s is mounted by kubesoloctl itself", entry, d)
		}
		if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
			return nil, fmt.Errorf("invalid container mount %q: the mode must be ro or rw", entry)
		}
		binds = append(binds, entry)
	}
	return binds, nil
}

// userBinds returns the mounts of a container that came from
// --container-mounts, leaving out the ones containerBinds adds itself.
func userBinds(binds []string) []string {
	var out []string
	for _, b := range binds {
		parts := strings.Split(b, ":")
		if len(parts) >= 2 && (parts[1] == containerDataDir || parts[1] == containerConfigDir || parts[1] == "/lib/modules") {
			continue
		}
		out = append(out, b)
	}
	return out
}
