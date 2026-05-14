# KubeSolo Installation Guide

This guide provides installation methods for KubeSolo on various systems, including industrial devices, embedded systems, and custom Linux distributions.

## Overview

KubeSolo now includes universal installation support that automatically detects your system and creates appropriate service configurations. The installer works on standard Linux distributions with systemd as well as industrial devices with custom init systems built using Yocto, Buildroot, or similar tools.

## Installation Options

### 1. Universal Installer (`install.sh`) - **Recommended**

**Best for:** All systems - automatically detects and adapts to your environment

The main installer automatically detects your init system, libc type, and creates appropriate service files. It downloads the correct binary variant for your system:

```bash
# Standard installation (works everywhere)
curl -sfL https://get.kubesolo.io | sudo sh -

# Or download and run directly
curl -sfL https://raw.githubusercontent.com/portainer/kubesolo/develop/install.sh | sudo sh -

# With options
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --version=v1.1.6 \
  --path=/opt/kubesolo \
  --run-mode=service

# With proxy
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --proxy=http://proxy.company.com:8080
```

**Automatic Detection:**
- **Init System**: systemd, SysV init, OpenRC, s6, runit, upstart
- **libc Type**: Automatically downloads glibc or musl binaries
- **Architecture**: amd64, arm64, arm, riscv64

**Supported Systems:**
- **glibc systems**: Ubuntu, CentOS, Debian, RHEL, SUSE, etc.
- **musl systems**: Alpine Linux, Void Linux (musl), embedded systems
- **Mixed environments**: Automatically selects correct binary variant

**Run Modes:**
- `service` (default): Creates proper service files for your init system
- `daemon`: Runs as background process with PID file
- `foreground`: Runs in foreground (for testing/debugging)

### 2. Minimal Installer (`install-minimal.sh`)

**Best for:** Extremely constrained busybox-based systems

This script provides basic installation with minimal dependencies for the most constrained environments:

```bash
# Download and run
wget -O - https://raw.githubusercontent.com/portainer/kubesolo/develop/install-minimal.sh | sh

# Or with environment variables
KUBESOLO_VERSION=v1.1.6 KUBESOLO_PATH=/opt/kubesolo sh install-minimal.sh
```

**Features:**
- Works with busybox utilities only
- Creates simple init script
- Provides `kubesolo-ctl` management tool
- Minimal dependencies (only requires `tar` and `wget`/`curl`)

### 3. Service Management (`kubesolo-service.sh`)

**Best for:** Managing KubeSolo across different init systems

Universal service management script that works with any supported init system:

```bash
# Download and make executable
curl -sfL https://raw.githubusercontent.com/portainer/kubesolo/develop/kubesolo-service.sh -o kubesolo-service.sh
chmod +x kubesolo-service.sh

# Usage
./kubesolo-service.sh start     # Start service
./kubesolo-service.sh stop      # Stop service
./kubesolo-service.sh restart   # Restart service
./kubesolo-service.sh status    # Check status
./kubesolo-service.sh logs      # View logs
./kubesolo-service.sh enable    # Enable at boot
./kubesolo-service.sh disable   # Disable at boot
```

## Environment Variables

All installers support these environment variables:

```bash
export KUBESOLO_VERSION="v1.1.6"           # Version to install
export KUBESOLO_PATH="/var/lib/kubesolo"        # Installation path
export KUBESOLO_PORTAINER_EDGE_ID="your-id"     # Portainer Edge ID
export KUBESOLO_PORTAINER_EDGE_KEY="your-key"   # Portainer Edge Key
export KUBESOLO_PORTAINER_EDGE_ASYNC="false"    # Async mode
export KUBESOLO_LOCAL_STORAGE="false"           # Enable local storage
export KUBESOLO_DEBUG="false"                   # Debug logging
export KUBESOLO_PPROF_SERVER="false"            # Enable pprof
export KUBESOLO_RUN_MODE="service"              # Run mode (universal installer only)
export KUBESOLO_PROXY="http://proxy.company.com:8080"  # Corporate proxy for HTTP/HTTPS requests
```

## Industrial Device Considerations

### 1. **Read-Only Root Filesystem**

Many industrial devices use read-only root filesystems. Consider:

```bash
# Install to writable partition
export KUBESOLO_PATH="/data/kubesolo"
curl -sfL https://get.kubesolo.io | sudo sh -s -- --path=/data/kubesolo

# Or use tmpfs for runtime data
export KUBESOLO_PATH="/tmp/kubesolo"
```

### 2. **Limited Storage**

For devices with limited storage:

```bash
# Use minimal installer
./install-minimal.sh

# Disable local storage provisioner
export KUBESOLO_LOCAL_STORAGE="false"
```

### 3. **No Internet Access**

For air-gapped installations:

```bash
# Pre-download the binary
wget https://github.com/portainer/kubesolo/releases/download/v1.1.6/kubesolo-v1.1.6-linux-arm64.tar.gz

# Extract and install manually
tar -xzf kubesolo-*.tar.gz
mv kubesolo /usr/local/bin/
chmod +x /usr/local/bin/kubesolo

# Create basic service (example for SysV init)
cat > /etc/init.d/kubesolo << 'EOF'
#!/bin/sh
case "$1" in
    start) /usr/local/bin/kubesolo --path=/var/lib/kubesolo & ;;
    stop) pkill kubesolo ;;
    *) echo "Usage: $0 {start|stop}" ;;
esac
EOF
chmod +x /etc/init.d/kubesolo
```

### 4. **Corporate Proxy Support**

For environments requiring corporate proxy access:

```bash
# Using command-line flag
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --proxy=http://proxy.company.com:8080

# Using environment variable
export KUBESOLO_PROXY="http://proxy.company.com:8080"
curl -sfL https://get.kubesolo.io | sudo sh -

# Combined with other options
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --proxy=http://proxy.company.com:8080 \
  --version=v1.1.6 \
  --path=/opt/kubesolo
```

The proxy configuration automatically sets the following environment variables for all supported init systems:
- `HTTP_PROXY=http://your.proxy.server:port`
- `HTTPS_PROXY=http://your.proxy.server:port`
- `NO_PROXY=localhost,127.0.0.1`

**Supported across all init systems:**
- systemd (via Environment directives)
- SysV init (via export statements)
- OpenRC (via export statements)
- s6 (via export statements)
- runit (via export statements)
- upstart (via env directives)
- daemon mode (via export statements)
- foreground mode (via export statements)

### 5. **Custom Init Systems**

For completely custom init systems:

```bash
# Run in daemon mode
export KUBESOLO_RUN_MODE="daemon"
curl -sfL https://get.kubesolo.io | sudo sh -

# Or run manually
/usr/local/bin/kubesolo --path=/var/lib/kubesolo > /var/log/kubesolo.log 2>&1 &
echo $! > /var/run/kubesolo.pid
```

## Architecture Support

All installers support multiple architectures with automatic binary selection:

### glibc Binaries (Standard)
- `x86_64` (amd64) - Ubuntu, CentOS, Debian, etc.
- `aarch64` (arm64) - ARM64 systems with glibc
- `armv7l` (arm) - ARM 32-bit systems with glibc
- `riscv64` - RISC-V 64-bit systems

### musl Binaries (Alpine Linux Compatible)
- `x86_64` (amd64) - Alpine Linux x86_64
- `aarch64` (arm64) - Alpine Linux ARM64

**Note:** The installer automatically detects your system type and downloads the appropriate binary. musl binaries are static and work on any musl-based system without additional dependencies.

## Troubleshooting

### Init System Detection Issues

```bash
# Check detected init system
curl -sfL https://get.kubesolo.io | sh -s -- --help

# Force specific mode
export KUBESOLO_RUN_MODE="daemon"
curl -sfL https://get.kubesolo.io | sudo sh -
```

### Service Not Starting

```bash
# Check logs
tail -f /var/log/kubesolo.log

# Check process
ps aux | grep kubesolo

# Check permissions
ls -la /usr/local/bin/kubesolo
ls -la /var/lib/kubesolo
```

### Kubeconfig Issues

```bash
# Check if kubeconfig exists
ls -la /var/lib/kubesolo/pki/admin/admin.kubeconfig

# Set environment variable
export KUBECONFIG=/var/lib/kubesolo/pki/admin/admin.kubeconfig

# Or copy to standard location
mkdir -p ~/.kube
cp /var/lib/kubesolo/pki/admin/admin.kubeconfig ~/.kube/config
```

## Examples for Specific Platforms

### Yocto/OpenEmbedded

```bash
# In your Yocto recipe
SRC_URI += "https://raw.githubusercontent.com/portainer/kubesolo/develop/install-minimal.sh"

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${WORKDIR}/install-minimal.sh ${D}${bindir}/
}
```

### Alpine Linux

```bash
# Alpine uses OpenRC and musl libc - installer detects both automatically
apk add curl
curl -sfL https://get.kubesolo.io | sh

# The installer will:
# 1. Detect OpenRC init system
# 2. Detect musl libc and download musl-compatible binary
# 3. Create appropriate OpenRC service file
```

**Alpine-specific features:**
- Automatically downloads musl-compatible static binary
- Creates OpenRC service configuration
- Works on both x86_64 and aarch64 Alpine systems
- No additional dependencies required

### Buildroot

```bash
# Add to your Buildroot package
define KUBESOLO_INSTALL_TARGET_CMDS
    $(INSTALL) -D -m 0755 $(KUBESOLO_PKGDIR)/install-minimal.sh $(TARGET_DIR)/usr/bin/
endef
```

## Support Matrix

| Platform | Universal Installer | Minimal Installer | Service Manager | Binary Type |
|----------|-------------------|------------------|-----------------|-------------|
| Ubuntu/Debian (glibc) | ✅ | ✅ | ✅ | glibc |
| CentOS/RHEL (glibc) | ✅ | ✅ | ✅ | glibc |
| Alpine Linux (musl) | ✅ | ✅ | ✅ | musl |
| Void Linux (musl) | ✅ | ✅ | ✅ | musl |
| systemd | ✅ | ✅ | ✅ | auto |
| SysV init | ✅ | ✅ | ✅ | auto |
| OpenRC | ✅ | ✅ | ✅ | auto |
| s6 | ✅ | ❌ | ✅ | auto |
| runit | ✅ | ❌ | ✅ | auto |
| upstart | ✅ | ❌ | ✅ | auto |
| busybox | ⚠️ | ✅ | ⚠️ | auto |
| custom | ❌ | ✅ | ❌ | manual |

✅ Full support  
⚠️ Limited support  
❌ Not supported  
auto = Automatically detects and downloads correct binary  
manual = Manual binary selection required  

## Contributing

To add support for additional init systems or platforms:

1. Add detection logic to `detect_init_system()` in `install.sh`
2. Implement service creation function
3. Add service management to `kubesolo-service.sh`
4. Test on target platform
5. Update documentation