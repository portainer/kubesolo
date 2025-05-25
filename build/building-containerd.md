# Building Containerd Binaries

This document describes how to build containerd binaries for ARM32 (ARMHF) using Docker-based cross-compilation.

## Prerequisites

- Docker installed and running
- The `containerd.Dockerfile` in the `build/` directory

## Building ARM32 Containerd Binaries

The `containerd.Dockerfile` provides a complete cross-compilation environment for building containerd binaries targeting ARM32 hard float (ARMHF) architecture.

### Build Process

1. **Build the Docker image** (runs natively on ARM64/AMD64 host):
   ```bash
   docker build -f build/containerd.Dockerfile -t containerd-arm32-cross .
   ```

2. **Extract the compiled archive**:
   ```bash
   docker create --name temp containerd-arm32-cross && \
   docker cp temp:/containerd-v2.0.4-linux-arm32.tar.gz . && \
   docker rm temp
   ```

## What Gets Built

The Dockerfile builds containerd with the following features enabled:
- **seccomp**: Security computing mode support
- **selinux**: SELinux security module support  
- **apparmor**: AppArmor security module support
- **btrfs_noversion**: Btrfs filesystem support (without version checking)

## Target Architecture

- **Platform**: linux/arm (32-bit)
- **ABI**: ARMHF (ARM Hard Float)
- **ARM Version**: ARMv7+ with hardware floating point unit
- **Cross-compiler**: `arm-linux-gnueabihf-gcc`

## Output

The build process produces a compressed archive `containerd-v2.0.4-linux-arm32.tar.gz` containing all the containerd binaries. After extraction, the binaries will be available in the `bin/` directory and can be used to replace the downloaded containerd binaries in your kubesolo distribution for ARM32 targets.

## Notes

- The build process uses containerd version `v2.0.4` by default (configurable via `CONTAINERD_VERSION` build arg)
- All necessary cross-compilation dependencies are included in the Docker image
- The binaries are statically linked where possible for better portability
