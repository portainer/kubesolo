# Building Pause Container Image

This document describes how to build a custom multi-architecture pause container image based on the Kubernetes pause container.

## Building Multi-Architecture Pause Image

The `.github/workflows/build-pause-image.yaml` workflow provides a complete cross-compilation and container build process for creating pause container images targeting multiple architectures.

### Build Process

1. **Trigger the workflow**:
   - Go to your GitHub repository
   - Navigate to **Actions** tab
   - Select **Build Pause Image** workflow
   - Click **Run workflow** button
   - Click **Run workflow** to confirm

2. **Monitor the build progress**:
   - Watch the workflow execution in the Actions tab
   - Each architecture builds in parallel
   - The manifest creation runs after all builds complete

3. **Pull the built image**:
   ```bash
   docker pull portainer/pause:latest
   ```

## What Gets Built

The workflow builds pause container images with the following characteristics:
- **Base Image**: `scratch` (minimal, security-focused)
- **Source**: Official Kubernetes pause container source code
- **Binary**: Statically compiled pause binary with version metadata
- **Size**: Minimal footprint (< 1MB per architecture)

## Target Architectures

The workflow builds for the following platforms:
- **linux/amd64**: x86_64 architecture
- **linux/arm64**: ARM 64-bit architecture  
- **linux/arm**: ARM 32-bit (ARMv7+ with hardware floating point)
- **linux/riscv64**: RISC-V 64-bit architecture

### Cross-Compilation Details

- **AMD64**: `x86_64-linux-gnu-gcc`
- **ARM64**: `aarch64-linux-gnu-gcc`
- **ARM32**: `arm-linux-gnueabihf-gcc` (ARMHF - ARM Hard Float)
- **RISCV64**: `riscv64-linux-gnu-gcc`

## Output

The build process produces:

1. **Single-architecture images** (intermediate):
   - `portainer/pause:linux-amd64`
   - `portainer/pause:linux-arm64`
   - `portainer/pause:linux-arm`
   - `portainer/pause:linux-riscv64`

2. **Multi-architecture manifest** (final):
   - `portainer/pause:latest` - automatically selects the correct architecture

## Notes

- The workflow uses **sparse Git checkout** to fetch only the pause directory from kubernetes/kubernetes repository
- All binaries are **statically linked** and **stripped** for minimal size
- The build process is **fully automated** and requires no manual intervention
- **Docker manifest** automatically handles platform selection when pulling the `:latest` tag
