package network

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
)

// maxNameservers is the Linux resolv.conf hard limit (MAXNS in libc/kubelet).
const maxNameservers = 3

var (
	rfc1123 = `^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`
)

// GetLocalIPs returns all non-loopback IPv4 addresses
func GetLocalIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	ips := []net.IP{}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP)
			}
		}
	}
	return append(ips, net.ParseIP("127.0.0.1")), nil
}

// ResolveNodeIP returns the node IP to advertise and whether it was explicitly
// pinned via a valid override. When override is a valid IPv4 it is used as-is
// and pinned is true — a value not bound to a local interface is still allowed
// but logged (e.g. a VIP). An empty or unparseable override falls back to
// auto-detection via GetNodeIP with pinned false, so a typo does not silently
// switch the caller into "pinned" behavior.
func ResolveNodeIP(override string) (ip string, pinned bool, err error) {
	if override == "" {
		ip, err = GetNodeIP()
		return ip, false, err
	}
	if !IsIPv4Address(override) {
		log.Warn().Str("component", "network").Str("node-ip", override).
			Msg("--node-ip is not a valid IPv4 address; ignoring it and auto-detecting the node IP")
		ip, err = GetNodeIP()
		return ip, false, err
	}
	if !isLocalIP(override) {
		log.Warn().Str("component", "network").Str("node-ip", override).
			Msg("--node-ip is not bound to a local interface; using it anyway (e.g. VIP)")
	}
	return override, true, nil
}

// GetNodeIP returns a non-loopback IPv4 address of the node, preferring a
// private (RFC 1918) address over a public one. On a host with several private
// addresses the first one encountered is returned, so the choice is not
// guaranteed to be stable across reboots — pin a specific address with
// --node-ip when that matters.
func GetNodeIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1", fmt.Errorf("failed to get node IP address: %v", err)
	}
	return selectNodeIP(addrs)
}

// selectNodeIP picks the node IP from the given interface addresses, preferring
// the first private IPv4 and falling back to the first non-loopback IPv4.
func selectNodeIP(addrs []net.Addr) (string, error) {
	var firstNonLoopback string
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		ipv4 := ipnet.IP.To4()
		if ipv4.IsPrivate() {
			return ipv4.String(), nil
		}
		if firstNonLoopback == "" {
			firstNonLoopback = ipv4.String()
		}
	}
	if firstNonLoopback != "" {
		return firstNonLoopback, nil
	}
	return "127.0.0.1", fmt.Errorf("could not find non-loopback IPv4 address")
}

// isLocalIP reports whether ip is bound to one of the host's interfaces.
func isLocalIP(ip string) bool {
	target := net.ParseIP(ip)
	locals, err := GetLocalIPs()
	if err != nil {
		return false
	}
	for _, l := range locals {
		if l.Equal(target) {
			return true
		}
	}
	return false
}

// IsIPv4Address returns true if the given string is a valid IPv4 address
func IsIPv4Address(ip string) bool {
	return net.ParseIP(ip) != nil && net.ParseIP(ip).To4() != nil
}

// IsDNSName returns true if the given string is a valid DNS name
// it follows the RFC 1123 specification for DNS names
func IsDNSName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}

	// RFC 1123 compliant DNS name pattern
	matched, _ := regexp.MatchString(rfc1123, name)
	return matched
}

// instanceMetadataServiceIP is the IP used by cloud providers (AWS, GCP, Azure)
// for instance metadata services. This must be allowed as a valid nameserver
// even though it is a link-local address.
var instanceMetadataServiceIP = net.ParseIP("169.254.169.254")

// GetHostResolvConf returns the path to a resolv.conf file that contains
// real upstream nameservers suitable for use by pods. It checks common resolv.conf
// locations and validates that they contain usable nameservers (global unicast).
// If no valid resolv.conf is found, it generates a fallback with public DNS servers.
func GetHostResolvConf(dataDir string, containerMode bool) string {
	if containerMode {
		log.Info().Str("component", "network").
			Msg("running in container mode - using /dev/null for resolv.conf to prevent host DNS leakage into pods")
		return "/dev/null"
	}

	resolvConfs := []string{"/etc/resolv.conf", "/run/systemd/resolve/resolv.conf"}
	for _, conf := range resolvConfs {
		if isValidResolvConf(conf) {
			if conf != "/etc/resolv.conf" {
				log.Info().Str("component", "network").
					Msgf("/etc/resolv.conf has unusable nameservers, using %s", conf)
			}
			// Sanitize if nameserver count exceeds the Linux hard limit of 3.
			sanitized, err := sanitizeResolvConf(conf, dataDir)
			if err != nil {
				log.Warn().Str("component", "network").Err(err).
					Msg("failed to sanitize resolv.conf, using original")
				return conf
			}
			return sanitized
		}
	}

	// No valid resolv.conf found — generate a fallback
	resolvConf := filepath.Join(dataDir, "resolv.conf")
	if err := os.WriteFile(resolvConf, []byte("nameserver 8.8.8.8\nnameserver 1.1.1.1\n"), 0644); err != nil {
		log.Error().Str("component", "network").Msgf("failed to write fallback resolv.conf: %v", err)
		return "/etc/resolv.conf"
	}
	log.Warn().Str("component", "network").
		Msg("host resolv.conf includes loopback or link-local nameservers - using autogenerated resolv.conf with 8.8.8.8 and 1.1.1.1")
	return resolvConf
}

// sanitizeResolvConf reads the resolv.conf at srcPath, deduplicates nameservers,
// and caps them to maxNameservers. If no sanitization is needed, srcPath is returned
// unchanged. Otherwise a sanitized copy is written to dataDir/resolv.conf.
func sanitizeResolvConf(srcPath, dataDir string) (string, error) {
	file, err := os.Open(srcPath)
	if err != nil {
		return srcPath, nil
	}
	defer func() { _ = file.Close() }()

	nameserverRe := regexp.MustCompile(`^nameserver\s+([^\s]*)`)

	var otherLines []string
	var nameservers []string
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		match := nameserverRe.FindStringSubmatch(line)
		if len(match) == 2 {
			ns := match[1]
			if !seen[ns] {
				seen[ns] = true
				nameservers = append(nameservers, ns)
			}
			// duplicate/excess nameserver lines are dropped
			continue
		}
		otherLines = append(otherLines, line)
	}
	if err := scanner.Err(); err != nil {
		return srcPath, fmt.Errorf("scanning resolv.conf: %w", err)
	}

	if len(nameservers) <= maxNameservers {
		return srcPath, nil
	}

	omitted := nameservers[maxNameservers:]
	nameservers = nameservers[:maxNameservers]
	log.Warn().Str("component", "network").
		Strs("omitted_nameservers", omitted).
		Msgf("host resolv.conf has more than %d nameservers; capping to prevent kubelet warnings", maxNameservers)

	var b strings.Builder
	for _, ns := range nameservers {
		b.WriteString("nameserver ")
		b.WriteString(ns)
		b.WriteString("\n")
	}
	for _, line := range otherLines {
		b.WriteString(line)
		b.WriteString("\n")
	}

	destPath := filepath.Join(dataDir, "resolv.conf")
	if err := os.WriteFile(destPath, []byte(b.String()), 0644); err != nil {
		return srcPath, fmt.Errorf("writing sanitized resolv.conf: %w", err)
	}
	return destPath, nil
}

// isValidResolvConf checks whether a resolv.conf file exists and contains
// at least one valid upstream nameserver (global unicast address).
func isValidResolvConf(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()

	nameserver := regexp.MustCompile(`^nameserver\s+([^\s]*)`)
	scanner := bufio.NewScanner(file)
	foundNameserver := false
	for scanner.Scan() {
		ipMatch := nameserver.FindStringSubmatch(scanner.Text())
		if len(ipMatch) == 2 {
			if !isValidNameserver(ipMatch[1]) {
				return false
			}
			foundNameserver = true
		}
	}
	if err := scanner.Err(); err != nil {
		return false
	}
	return foundNameserver
}

// isValidNameserver returns true if the IP is a valid upstream resolver address.
// Resolver IPs must be global unicast, with the exception of the cloud instance
// metadata service IP (169.254.169.254), which some cloud providers require
// for private DNS to work properly.
func isValidNameserver(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	if !ip.IsGlobalUnicast() && !ip.Equal(instanceMetadataServiceIP) {
		return false
	}
	return true
}
