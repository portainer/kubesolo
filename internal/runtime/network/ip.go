package network

import (
	"fmt"
	"net"
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
