# kubesolo 

Ultra-lightweight, OCI-compliant, single-node Kubernetes built for constrained environments. No clustering. No etcd. Just what you need to run real workloads on real hardware.

## Overview

KubeSolo is designed for devices at the farthest layer of the network, such as IoT, IIoT, and embedded systems. The image illustrates the three main layers of modern distributed infrastructure:
1. Cloud (Data Centers)
Scale: Thousands of nodes
Examples: Amazon EKS, Google Kubernetes Engine, VMware Tanzu, Sidero
Purpose: Centralized, large-scale compute and storage
2. FOG (Distributed Nodes)
Scale: Millions of nodes
Examples: K3s, MicroK8s, Sidero, K0S
Purpose: Distributed compute closer to the edge, often for latency-sensitive or regional workloads
3. Edge (Devices)
Scale: Billions of devices
Example: KubeSolo
Purpose: Ultra-lightweight Kubernetes for resource-constrained environments (IoT gateways, industrial controllers, smart devices, etc.)
KubeSolo sits at the very bottom of this stack, providing a simple, single-node Kubernetes experience for the edge, where minimal resources and offline operation are critical.

![KubeSolo Overview](assets/kubesolo-overview.png)

## What is this?

KubeSolo is a production-ready single-node Kubernetes distribution with the following changes:

* It is packaged as a single binary
* It uses SQLite (via Kine) as the default storage backend
* It wraps Kubernetes and other components in a single, simple launcher
* It is secure by default with reasonable defaults for lightweight environments
* It has minimal OS dependencies (just a sane kernel and cgroup mounts needed)
* It eliminates the need for complex multi-node setup by providing a single-node solution

KubeSolo bundles the following technologies together into a single cohesive distribution:

* containerd & runc for container runtime
* CoreDNS for DNS resolution
* Kine for SQLite-based storage

## What's with the name?

KubeSolo is designed to be a single-node Kubernetes distribution, hence the "Solo" in the name. It's meant to be simple, lightweight, and perfect for development, testing, or small production workloads that don't require the complexity of a multi-node cluster.

## Is this a fork?

No, it's a distribution. A fork implies continued divergence from the original. This is not KubeSolo's goal or practice. KubeSolo explicitly intends not to change any core Kubernetes functionality. We seek to remain as close to upstream Kubernetes as possible by leveraging the k3s forked Kubernetes. However, we maintain a small set of patches important to KubeSolo's use case and deployment model.

## How is this lightweight or smaller than upstream Kubernetes?

There are three major ways that KubeSolo is lighter weight than upstream Kubernetes:

* The memory footprint to run is smaller
* The binary, which contains all the non-containerized components needed to run a cluster, is smaller
* The Kubernetes Scheduler does not exist, instead, it is replaced by a custom Webhook called `NodeSetter`

The memory footprint is reduced primarily by:

* Running many components inside of a single process
* Using SQLite instead of `etcd`
* Optimizing resource limits for single-node usage
* Replacing the Kubernetes Scheduler with `NodeSetter` 

## Why is the binary size big compared to other distributions?

KubeSolo is designed specifically for IoT or IIoT devices, such as embedded systems, which typically lack internet connectivity. To address this limitation, KubeSolo is equipped with all the necessary components to ensure it is offline ready.

## Getting Started

### Quick Install

```bash
# Download and install KubeSolo
curl -sfL https://get.kubesolo.io | sudo sh -
```

A kubeconfig file is written to `/var/lib/kubesolo/pki/admin/admin.kubeconfig` and the service is automatically started.

Note: If you’re running KubeSolo on a device with less than 512MB of RAM, it’s strongly advised to interact with KubeSolo using the `kubectl` command-line tool installed externally.

## Flags

KubeSolo supports the following command-line flags:

| Flag | Environment Variable | Description | Default |
|------|-------------|---------|---------|
| `--path` | `KUBESOLO_PATH` | Path to the directory containing the kubesolo configuration files | `/var/lib/kubesolo` |
| `--portainer-edge-id` | `KUBESOLO_PORTAINER_EDGE_ID` | Portainer Edge ID | `""` |
| `--portainer-edge-key` | `KUBESOLO_PORTAINER_EDGE_KEY` | Portainer Edge Key | `""` |
| `--portainer-edge-async` | `KUBESOLO_PORTAINER_EDGE_ASYNC` | Enable Portainer Edge Async Mode | `false` |
| `--local-storage` | `KUBESOLO_LOCAL_STORAGE` | Enable local storage | `true` |
| `--debug` | `KUBESOLO_DEBUG` | Enable debug logging | `false` |
| `--pprof-server` | `KUBESOLO_PPROF_SERVER` | Enable pprof server for profiling | `false` |

Example:

To config KubeSolo to use Portainer Edge, you can use the following command:

```bash
curl -sfL https://get.kubesolo.io | KUBESOLO_PORTAINER_EDGE_ID=your-portainer-edge-id KUBESOLO_PORTAINER_EDGE_KEY=your-portainer-edge-key sudo -E sh
```

## Documentation

Please see the [documentation](https://kubesolo.io/documentation) for complete documentation.

## Building from Source

### Prerequisites

- Go 1.24 or later
- Docker (for ARM builds and dependency management)
- Cross-compilation toolchains (optional, for cross-platform builds)

### Install Cross-Compilation Toolchains

To build for multiple architectures, install the required cross-compilation toolchains:

```bash
make install-cross-compilers
```

This installs:
- `gcc-aarch64-linux-gnu` (for ARM64)
- `gcc-x86-64-linux-gnu` (for AMD64)
- `gcc-arm-linux-gnueabihf` (for ARM/ARMHF)

### Basic Build

Build for your current platform (outputs to `./dist/kubesolo`):

```bash
make build
```

### Cross-Platform Builds

Build for specific architectures using environment variables:

```bash
# Build for ARM64
GOARCH=arm64 make build

# Build for AMD64  
GOARCH=amd64 make build

# Build for ARM (ARMHF)
GOARCH=arm make build
```

### Custom Output Path

Specify a custom output path using the `OUTPUT` variable:

```bash
# Custom filename
OUTPUT=./kubesolo-custom make build

# Platform-specific naming
GOARCH=arm OUTPUT=./dist/kubesolo-arm make build

# Different directory
OUTPUT=./bin/kubesolo make build
```

### Combined Examples

```bash
# Build ARM binary with custom name
GOARCH=arm OUTPUT=./dist/kubesolo-linux-arm make build

# Build AMD64 binary for CI/CD
GOARCH=amd64 OUTPUT=./artifacts/kubesolo-linux-amd64 make build
```

### Development

For development and testing, you can run KubeSolo directly without building:

```bash
# Download dependencies first (only needed once)
make deps

# Run KubeSolo in development mode
make dev
```

### Notes

- **ARM builds**: For ARM architecture, containerd binaries are built using Docker cross-compilation for optimal compatibility
- **Dependencies**: The build process automatically downloads required dependencies (containerd, runc, CNI plugins) for the target architecture
- **CGO**: All builds use CGO for better performance and compatibility with system libraries

## Community

### Getting involved

GitHub Issues - Submit your issues and feature requests via GitHub.

## Release cadence

KubeSolo maintains pace with upstream Kubernetes releases but rely on the forked version from `k3s`. Our goal is to release patch releases within one week, and new minors within 30 days.

## Security

Security issues in KubeSolo can be reported by sending an email to security@portainer.io.

## TradeMark

KubeSolo and the KubeSolo logo are registered trademarks of Portaner.io Limited