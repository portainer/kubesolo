//go:build !linux

package network

// CheckFirewall is Linux only; KubeSolo does not run on the host elsewhere.
func CheckFirewall(string, string, int) error { return nil }
