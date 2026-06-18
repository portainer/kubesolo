package service

import (
	"fmt"
	"strings"

	"github.com/docker/go-connections/nat"
)

// ParseContainerPorts turns a --container-ports value into the exposed-port set
// and host port bindings to publish on the KubeSolo container.
//
// The value is a comma-separated list of entries. Each entry is one of:
//
//	9001              bare port  → host 9001 : container 9001
//	9000-9100         range      → host range : same container range (1:1)
//	8080:80           explicit   → host 8080  : container 80
//	127.0.0.1:80:80   bound IP   → only that host IP
//	53/udp            protocol   → tcp is assumed when omitted
//
// Bare ports and ranges map host==container (the common case for exposing a
// workload, like Kind's extraPortMappings). Explicit host:container and
// ip:host:container forms are passed through to Docker's standard parser.
func ParseContainerPorts(spec string) (nat.PortSet, nat.PortMap, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil, nil
	}

	var specs []string
	for _, entry := range strings.Split(spec, ",") {
		normalized, err := normalizePortSpec(entry)
		if err != nil {
			return nil, nil, err
		}
		if normalized != "" {
			specs = append(specs, normalized)
		}
	}

	exposed, bindings, err := nat.ParsePortSpecs(specs)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid --container-ports %q: %w", spec, err)
	}
	return exposed, bindings, nil
}

// normalizePortSpec converts a single user entry into a Docker port spec string
// (the same syntax accepted by `docker run -p`). A bare port or range is
// rewritten as host==container; explicit mappings are passed through untouched.
func normalizePortSpec(entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", nil
	}

	// Split off an optional /tcp or /udp protocol suffix.
	proto := "tcp"
	base := entry
	if i := strings.LastIndex(entry, "/"); i >= 0 {
		base = entry[:i]
		proto = strings.ToLower(entry[i+1:])
		if proto != "tcp" && proto != "udp" {
			return "", fmt.Errorf("invalid protocol %q in port spec %q (want tcp or udp)", proto, entry)
		}
	}

	if base == "" {
		return "", fmt.Errorf("empty port in spec %q", entry)
	}

	// An explicit mapping (host:container or ip:host:container) already carries a
	// colon — hand it to Docker's parser verbatim. A bare port or range has no
	// colon and is mapped host==container.
	if !strings.Contains(base, ":") {
		base = base + ":" + base
	}

	return base + "/" + proto, nil
}
