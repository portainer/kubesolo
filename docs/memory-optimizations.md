# KubeSolo Memory Optimizations

This document details memory optimizations implemented in KubeSolo to reduce its overall memory footprint for constrained environments.

## Core Runtime Optimizations

### Memory Management

- **Reduced Memory Limit**: Lowered the default memory limit from 100MB to 75MB
  - Location: `types/const.go`
  - Impact: Sets a lower soft limit for Go runtime, encouraging more frequent garbage collection

- **Increased Garbage Collection Frequency**: Reduced GC percentage from 50 to 30
  - Location: `types/const.go`
  - Impact: More aggressive garbage collection to free unused memory sooner

### Timeout Reductions

- **Reduced Context Timeouts**: Lowered default timeout from 30s to 15s
  - Location: `types/const.go`
  - Impact: Resources held during context operations are released faster

- **Reduced ContainerD Timeout**: Lowered from 30s to 15s
  - Location: `types/const.go`
  - Impact: Faster recovery and resource release when operations time out

## Database and Storage Optimizations

### SQLite Connection Pool

- **Reduced Connection Pool Size**: Lowered connection limits in Kine (SQLite storage)
  - Location: `pkg/kine/config.go`
  - Changes:
    - `MaxIdle`: Reduced from 5 to 2
    - `MaxOpen`: Reduced from 5 to 3
  - Impact: Fewer concurrent database connections, reducing memory overhead

## Logging Optimizations

- **Buffer Size Limits**: Added buffer pool size limits to reduce memory allocation for logs
  - Location: `internal/logging/logging.go`
  - Changes:
    - Set `zerolog.BufferPoolSize` to 2048
    - Simplified field names for more compact log representation

- **Stack Trace Optimization**: Removed full stack traces from default logging
  - Location: `internal/logging/logging.go`
  - Impact: Significantly reduces memory used for error logging

## Kubernetes Component Optimizations

### Kubelet Configuration

- **Reduced API Server Load**: Lowered request limits to the API server
  - Location: `pkg/kubernetes/kubelet/config.go`
  - Changes:
    - `kubeAPIQPS`: Reduced from 2 to 1
    - `kubeAPIBurst`: Reduced from 3 to 2

- **Worker Thread Limitation**: Added worker loop size limitation
  - Location: `pkg/kubernetes/kubelet/config.go`
  - Added `workerLoopSize: 1` to limit concurrent operations

- **Optimized Image Management**:
  - Location: `pkg/kubernetes/kubelet/config.go`
  - Changes:
    - `imageGCHighThresholdPercent`: Reduced from 95% to 90%
    - `imageGCLowThresholdPercent`: Reduced from 80% to 75%
    - Earlier cleanup of unused container images

- **Limited Network Operations**:
  - Location: `pkg/kubernetes/kubelet/config.go`
  - Changes:
    - `registryBurst`: Reduced from 2 to 1
    - `eventBurst`: Reduced from 2 to 1
  - Impact: Reduced memory usage during concurrent network operations

## Results

These optimizations collectively reduce KubeSolo's memory footprint, making it more suitable for constrained environments such as IoT or IIoT devices. The changes focus on:

1. More efficient garbage collection
2. Reduced connection pooling
3. Optimized logging
4. Limited concurrent operations
5. Faster resource cleanup

## Implementation Notes

These optimizations were selected to balance memory reduction with maintaining system stability. All changes are configurable through the constants defined in the codebase, allowing for adjustment based on specific deployment needs.

The optimizations aim to provide the best memory footprint while preserving core functionality of the Kubernetes distribution.