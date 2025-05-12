# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

KubeSolo is an ultra-lightweight, OCI-compliant, single-node Kubernetes distribution built for constrained environments such as IoT or IIoT devices. It bundles all necessary components into a single binary with SQLite as the storage backend via Kine.

## Build and Development Commands

### Building the Project

```bash
# Build KubeSolo
make build

# Build for a specific platform
GOOS=linux GOARCH=arm64 make build

# Build using Docker (for cross-compilation or environment isolation)
make build-using-image
```

### Development Commands

```bash
# Format and lint code
make lint

# Run KubeSolo in development mode
make dev

# Clean build artifacts
make clean

# Download dependencies (binaries and container images)
make deps
```

### Running KubeSolo

```bash
# Build and run (requires sudo)
make run

# Run with specific configuration
sudo ./dist/kubesolo --path /custom/path --debug
```

## Architecture Overview

KubeSolo is architected as a coordinated set of services started in sequence:

1. **ContainerD**: Container runtime
2. **Kine**: Kubernetes storage backend using SQLite
3. **API Server**: Kubernetes control plane
4. **Controller Manager**: Core Kubernetes controllers 
5. **Kubelet**: Node agent for managing containers
6. **Kube Proxy**: Network routing for Kubernetes services

The startup sequence is orchestrated in `cmd/kubesolo/main.go`, with each component signaling readiness before dependent components start.

### Key Components

- **Embedded Binaries**: ContainerD, runc, CNI plugins, and container images are embedded in the binary
- **PKI System**: Generates and manages certificates for secure communication
- **CoreDNS**: Deployed automatically for DNS resolution
- **Portainer Integration**: Optional deployment of Portainer Edge Agent

## Directory Structure

- `/cmd/kubesolo`: Main application entry point
- `/internal/`: Internal packages not intended for external use
  - `/config`: CLI flags and configuration
  - `/core/embedded`: Management of embedded binaries
  - `/core/pki`: Certificate generation and management
- `/pkg/`: Public packages and Kubernetes components
  - `/components`: Additional components like CoreDNS and Portainer
  - `/kine`: SQLite storage backend
  - `/kubernetes`: Kubernetes component implementations
  - `/runtime`: Container runtime implementation
- `/types`: Shared types and constants

## Development Patterns

1. Services follow a common pattern with:
   - Constructor (`NewService`)
   - `Run` method to start the service
   - Ready channel to signal initialization completion

2. Component dependencies are managed through ready channels passed to dependent services.

3. The `types.Embedded` struct is used throughout to provide paths and configuration.

## Common Tasks

- **Adding a new flag**: Add to `internal/config/flags/flags.go`
- **Modifying service startup**: Update service list in `cmd/kubesolo/main.go`
- **Changing component versions**: Update in `build/download-deps.sh`