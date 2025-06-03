package network

import (
	"fmt"
	"net"
	"regexp"
)

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

// GetNodeIP returns the first non-loopback IP address of the node
func GetNodeIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.To4().String(), nil
		}
	}

	return "127.0.0.1", fmt.Errorf("could not find non-loopback private IP address")
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
