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
			ip := ipnet.IP.To4()

			// Check if it's a private IP address (RFC 1918)
			// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
			if (ip[0] == 10) ||
				(ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
				(ip[0] == 192 && ip[1] == 168) {
				return ip.String(), nil
			}
		}
	}

	return "127.0.0.1", fmt.Errorf("could not find non-loopback private IP address")
}
