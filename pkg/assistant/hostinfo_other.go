//go:build !linux

package assistant

func hostInfo(string) HostInfo { return HostInfo{} }
