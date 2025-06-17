# KubeSolo Installation Guide

This guide provides installation methods for KubeSolo on various systems, including industrial devices, embedded systems, and custom Linux distributions.

## Overview

KubeSolo now includes universal installation support that automatically detects your system and creates appropriate service configurations. The installer works on standard Linux distributions with systemd as well as industrial devices with custom init systems built using Yocto, Buildroot, or similar tools.

## Installation Options

### 1. Universal Installer (`install.sh`) - **Recommended**

**Best for:** All systems - automatically detects and adapts to your environment

The main installer now automatically detects your init system and creates appropriate service files:

```bash
# Standard installation (works everywhere)
curl -sfL https://get.kubesolo.io | sudo sh -

# Or download and run directly
curl -sfL https://raw.githubusercontent.com/portainer/kubesolo/develop/install.sh | sudo sh -

# With options
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --version=v0.1.5-beta \
  --path=/opt/kubesolo \
  --run-mode=service
```

**Supported Init Systems:**
- systemd (standard Linux distributions)
- SysV init (older distributions, some embedded systems)
- OpenRC (Alpine Linux, Gentoo)
- s6 (some embedded distributions)
- runit (Void Linux, some embedded systems)
- upstart (older Ubuntu versions)

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
KUBESOLO_VERSION=v0.1.5-beta KUBESOLO_PATH=/opt/kubesolo sh install-minimal.sh
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
export KUBESOLO_VERSION="v0.1.5-beta"           # Version to install
export KUBESOLO_PATH="/var/lib/kubesolo"        # Installation path
export KUBESOLO_PORTAINER_EDGE_ID="your-id"     # Portainer Edge ID
export KUBESOLO_PORTAINER_EDGE_KEY="your-key"   # Portainer Edge Key
export KUBESOLO_PORTAINER_EDGE_ASYNC="false"    # Async mode
export KUBESOLO_LOCAL_STORAGE="false"           # Enable local storage
export KUBESOLO_DEBUG="false"                   # Debug logging
export KUBESOLO_PPROF_SERVER="false"            # Enable pprof
export KUBESOLO_RUN_MODE="service"              # Run mode (universal installer only)
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
wget https://github.com/portainer/kubesolo/releases/download/v0.1.5-beta/kubesolo-v0.1.5-beta-linux-arm64.tar.gz

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

### 4. **Custom Init Systems**

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

All installers support:
- `x86_64` (amd64)
- `aarch64` (arm64)
- `armv7l` (arm)

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
# Alpine uses OpenRC - universal installer detects this automatically
apk add curl
curl -sfL https://get.kubesolo.io | sh
```

### Buildroot

```bash
# Add to your Buildroot package
define KUBESOLO_INSTALL_TARGET_CMDS
    $(INSTALL) -D -m 0755 $(KUBESOLO_PKGDIR)/install-minimal.sh $(TARGET_DIR)/usr/bin/
endef
```

## Support Matrix

| Platform | Universal Installer | Minimal Installer | Service Manager |
|----------|-------------------|------------------|-----------------|
| systemd | ✅ | ✅ | ✅ |
| SysV init | ✅ | ✅ | ✅ |
| OpenRC | ✅ | ✅ | ✅ |
| s6 | ✅ | ❌ | ✅ |
| runit | ✅ | ❌ | ✅ |
| upstart | ✅ | ❌ | ✅ |
| busybox | ⚠️ | ✅ | ⚠️ |
| custom | ❌ | ✅ | ❌ |

✅ Full support  
⚠️ Limited support  
❌ Not supported  

## Contributing

To add support for additional init systems or platforms:

1. Add detection logic to `detect_init_system()` in `install.sh`
2. Implement service creation function
3. Add service management to `kubesolo-service.sh`
4. Test on target platform
5. Update documentation