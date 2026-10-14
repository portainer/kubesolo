# KubeSolo triage knowledge

## Reading an incident
- trigger: "did not shut down cleanly" means the previous run crashed, hit a fatal error, was killed or lost power; recentErrors are that run's last errors. "unhealthy for 5m0s: X" names components that failed every health check for 5 minutes.
- components: runtime = containerd, kine = the SQLite datastore (in place of etcd), apiserver, controller, kubelet, coredns, webhook. "ok" is healthy.
- When several components fail, find the first in this chain: runtime and kine, then apiserver, then controller, kubelet, coredns and webhook. kine down takes the apiserver down with it.
- host: diskUsedPercent of 90 or more is a problem by itself; memoryAvailableMiB under 200 means memory pressure; a small uptimeSeconds means the host just booted.
- Base the diagnosis on the evidence. If nothing below matches, say so: action report_only, confidence low.

## Actions
- restart_component: a component is stuck but its data is sound.
- repair_database: the kine database is damaged. KubeSolo repairs it on start with storage.dbWALRepair: true.
- prune_images: the disk is full of unused images.
- clean_orphaned_containers: pods run twice, or containers outlive KubeSolo.
- free_disk_space: the disk is full of anything other than images.
- report_only: a person must act: host configuration, hardware, network or an unsupported OS.

## Harmless lines
These are logged by every healthy start and never show a fault. KubeSolo removes any log line or event containing one of the quoted texts below before you read the incident, so quote only exact text that is never a fault.
- "Failed to parse etcd version", "checkpoint is not found", "required revision has been compacted", "Too large resource version".
- "Could not construct pre-rendered responses", "Error removing old endpoints from kubernetes service", "namespaces \"kube-system\" not found", "namespaces \"default\" not found".
- "Image garbage collection failed once. Stats initialization may not have completed yet", "Skipping pod synchronization", "PLEG is not healthy: pleg has yet to be successful", "eviction manager: failed to check if we have separate container filesystem", "Kube-proxy configuration may be incomplete".
- "cleaned stale containerd artifact", "removed stale system containerd socket", "Housekeeping took longer than expected".
- Event "InvalidDiskCapacity: invalid capacity 0 on image filesystem" at node start.
- "Failed to create existing container".

## KubeSolo failures

### Stopped without a fault
Signs: trigger "did not shut down cleanly", and recentErrors hold only harmless lines, or none.
Cause: KubeSolo was killed, or the host lost power or was reset. Nothing points to damage.
Action: report_only, confidence low.

### Damaged datastore
Signs: at least one of these errors: "database disk image is malformed", "file is not a database", "UID in precondition", "Failed to update lease", "poststarthook/rbac/bootstrap-roles failed", "provided IP is already allocated" for CoreDNS.
Cause: SQLite was left inconsistent by a power loss or kill.
Action: repair_database.

### Datastore unreachable or too slow
Signs: apiserver fails with "context deadline exceeded" or "connection refused" to 127.0.0.1:2379, "Error creating leases", "apiserver /healthz check failed", "shutdown requested before apiserver was ready"; KubeSolo restarts in a loop; kine unhealthy.
Cause: kine cannot answer in time: slow storage such as an SD card, a full disk, or a blocked loopback port.
Action: free_disk_space if the disk is 90% or more used, otherwise restart_component; report_only if it keeps recurring (slow storage).

### Container runtime stuck or slow
Signs: runtime unhealthy; "containerd.sock: connect: connection refused" repeated; "failed to import images", "(*service).Write failed", "failed to prepare extraction snapshot", "context deadline exceeded" while importing; "StopContainer ... failed", "ttrpc: received message on inactive stream".
Cause: containerd is overloaded, on slow storage, or out of disk.
Action: free_disk_space or prune_images when the disk is 90% or more used, otherwise restart_component.

### Orphaned or duplicate pods
Signs: pods running twice after a restart or upgrade; containers left in kubepods.slice after KubeSolo stopped; "ErrImageNeverPull" for images imported with ctr.
Cause: containers outlived KubeSolo, and the new run started them again.
Action: clean_orphaned_containers.

### Kubelet down
Signs: kubelet unhealthy; "127.0.0.1:10248/healthz ... connection refused"; event "NodeNotReady".
Cause: the kubelet stopped or is stuck.
Action: restart_component. If the errors show "status=403" with "Forbidden (user=system:node", RBAC is broken: repair_database.

### CoreDNS failing
Signs: coredns unhealthy; "dial tcp 10.43.0.1:443: connect: no route to host"; "Temporary failure resolving"; pods in CrashLoopBackOff right after boot.
Cause: the host firewall blocks pod traffic (firewalld, iptables REJECT rules), or the network was not up yet at boot.
Action: restart_component if the uptime is short (a boot race), otherwise report_only (firewall).

### Pod traffic blocked
Signs: components ok; "Host is unreachable" between pods; services fail while DNS works.
Cause: firewalld drops traffic on cni0.
Action: report_only: add cni0 to the trusted zone.

### Read-only or noexec filesystem
Signs: "read-only file system", "permission denied" running cni plugins, "not supported as upperdir".
Cause: /usr or /opt is read-only, /var is mounted noexec, or the filesystem cannot hold overlay layers.
Action: report_only.

### Certificate does not match the IP
Signs: "x509: certificate is valid for ... not ..."; "could not find non-loopback private IP address".
Cause: the host's IP changed since the certificates were made, or the host has no IP.
Action: report_only: delete /var/lib/kubesolo/pki and restart, or bring the network up.

### Missing kernel support
Signs: "cgroups ... controllers are missing", "cgroup_disable=memory", containers fail to start.
Cause: the kernel lacks the memory cgroup (Raspberry Pi OS default).
Action: report_only: add cgroup_enable=memory cgroup_memory=1 to the kernel command line.

### Unsupported host
Signs: "GLIBC_2.34' not found", "unsupported shim version", Docker installed alongside.
Cause: the OS is too old, or Docker's containerd conflicts.
Action: report_only.

### Memory pressure
Signs: memoryAvailableMiB under 200; "OOMKilled", "Memory cgroup out of memory: Killed process"; event "EvictionThresholdMet" or "SystemOOM".
Cause: the node runs more than its memory allows. KubeSolo itself using 150 to 500 MB is normal.
Action: report_only.

### Disk full
Signs: diskUsedPercent 90 or more; "no space left on device"; events "FreeDiskSpaceFailed", "ImageGCFailed", "EvictionThresholdMet" or "Evicted" with ephemeral-storage.
Cause: images, logs or volumes filled the disk.
Action: prune_images when image garbage collection failed, otherwise free_disk_space.

## Common Kubernetes problems (from events)
- "ErrImagePull", "ImagePullBackOff", "Failed to pull image": a wrong image name, missing registry credentials, or no network. report_only.
- "BackOff", "CrashLoopBackOff": the workload exits; its own logs say why. report_only.
- "OOMKilled": the container's memory limit is too low. report_only.
- "FailedScheduling" with "Insufficient cpu" or "Insufficient memory": the requests do not fit the node. report_only.
- "FailedMount", "FailedAttachVolume": a missing PVC, secret or config map, or local-path storage is not ready. report_only.
- "Unhealthy" for liveness or readiness probes: the application is slow or broken. report_only.
- "FailedCreatePodSandBox": the CNI or containerd failed; if the runtime is unhealthy see above, otherwise restart_component.
- "NodeHasDiskPressure", "NodeHasInsufficientMemory": see Disk full and Memory pressure.
