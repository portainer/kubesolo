package assistant

// Snapshot is the state of the node when an incident happened: everything the
// model gets to read about it.
type Snapshot struct {
	// Components maps each control plane component to "ok" or why its health
	// probe failed.
	Components map[string]string `json:"components,omitempty"`

	// RecentErrors are KubeSolo's latest error log lines, oldest first.
	RecentErrors []string `json:"recentErrors,omitempty"`

	// Events are recent Kubernetes warning events, when the API server answers.
	Events []string `json:"events,omitempty"`

	Host HostInfo `json:"host"`
}

// HostInfo is the host's state at the time.
type HostInfo struct {
	DiskUsedPercent int `json:"diskUsedPercent"`

	// DiskUsageMiB breaks the used space down, when the disk is too full:
	// images, assistantModel, podLogs, volumes, datastore and other.
	DiskUsageMiB map[string]int64 `json:"diskUsageMiB,omitempty"`

	MemoryAvailableMiB uint64 `json:"memoryAvailableMiB"`
	UptimeSeconds      int64  `json:"uptimeSeconds"`
}
