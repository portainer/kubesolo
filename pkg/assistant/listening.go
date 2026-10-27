package assistant

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// probePort finds the local port a failed probe names, as in
// `dial tcp 127.0.0.1:10248: connect: connection refused`.
var probePort = regexp.MustCompile(`(?:127\.0\.0\.1|localhost|\[::1\]):(\d+)`)

// tcpListen is a listening socket's state in /proc/net/tcp.
const tcpListen = "0A"

// listening says, for each failing component whose probe names a local port,
// whether anything listens on it. A port that listens yet refuses the probe
// points to something in between, such as a firewall rule, rather than to the
// component. It is nil when the listening ports are unknown.
func listening(components map[string]string, ports map[int]bool) map[string]bool {
	if ports == nil {
		return nil
	}
	var out map[string]bool
	for name, status := range components {
		m := probePort.FindStringSubmatch(status)
		if status == "ok" || m == nil {
			continue
		}
		port, _ := strconv.Atoi(m[1])
		if out == nil {
			out = map[string]bool{}
		}
		out[name] = ports[port]
	}
	return out
}

// listeningPorts reads the TCP ports the host listens on from procNet's tcp
// and tcp6, or returns nil when neither can be read.
func listeningPorts(procNet string) map[int]bool {
	var ports map[int]bool
	for _, name := range []string{"tcp", "tcp6"} {
		raw, err := os.ReadFile(filepath.Join(procNet, name))
		if err != nil {
			continue
		}
		if ports == nil {
			ports = map[int]bool{}
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[3] != tcpListen {
				continue
			}
			_, port, _ := strings.Cut(fields[1], ":")
			if p, err := strconv.ParseUint(port, 16, 16); err == nil {
				ports[int(p)] = true
			}
		}
	}
	return ports
}

// portBlocked says why inc's restart would not help, or returns "": a failing
// component listens on its port yet the probe is refused, so something in
// between, such as a firewall rule, refuses it, and a restart leaves that in
// place. A probe that times out is left alone: a hung component listens too,
// and a restart does help it.
func portBlocked(inc Incident) string {
	if inc.Triage == nil || inc.Triage.Action != "restart_component" {
		return ""
	}
	for _, name := range slices.Sorted(maps.Keys(inc.Snapshot.Listening)) {
		status := inc.Snapshot.Components[name]
		m := probePort.FindStringSubmatch(status)
		if !inc.Snapshot.Listening[name] || m == nil || !strings.Contains(status, "connection refused") {
			continue
		}
		return fmt.Sprintf("%s listens on port %s but refuses the probe, so something such as a firewall rule blocks the port, and a restart would not clear it", name, m[1])
	}
	return ""
}
