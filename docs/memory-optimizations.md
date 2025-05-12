# KubeSolo Memory Optimizations

This document details memory optimizations implemented in KubeSolo to reduce its overall memory footprint for constrained environments.

## Runtime Configuration via Environment Variables

KubeSolo now supports dynamic memory management configuration through environment variables, particularly useful for controlling memory usage during idle periods:

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `GOGC` | Garbage collection target percentage. Lower values trigger more frequent GC. | 30 |
| `GOMEMLIMIT` | Memory limit in bytes (e.g., `75000000` for ~75MB) | 75MB |
| `KUBESOLO_IDLE_MEMORY_CHECK` | Enable periodic memory release during idle periods | true |

Example usage:
```bash
# More aggressive garbage collection (20% threshold)
GOGC=20 ./dist/kubesolo

# Set memory limit to 50MB and enable idle memory check
GOMEMLIMIT=50000000 KUBESOLO_IDLE_MEMORY_CHECK=true ./dist/kubesolo
```

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

## Idle-State Memory Management

KubeSolo now includes features specifically designed to reduce memory usage during idle periods:

1. **Periodic Memory Release**: When enabled via `KUBESOLO_IDLE_MEMORY_CHECK=true`, KubeSolo will periodically release unused memory back to the operating system, even during idle periods.

2. **Idle Detection**: The system periodically checks for idle state (low activity) and triggers memory cleanup operations.

3. **Memory Limits During Idle**: By setting a low `GOGC` value, you can make garbage collection more aggressive during idle periods, keeping memory usage lower.

### Advanced Usage

For maximum memory efficiency during idle periods:

```bash
# Configuration for very memory-constrained environments
GOGC=20 GOMEMLIMIT=50000000 KUBESOLO_IDLE_MEMORY_CHECK=true ./dist/kubesolo
```

This configuration will:
- Run garbage collection very frequently (at 20% threshold)
- Cap memory usage at ~50MB
- Periodically release memory back to the OS during idle periods

These settings are particularly effective for IoT devices or edge computing scenarios where memory resources are severely constrained.