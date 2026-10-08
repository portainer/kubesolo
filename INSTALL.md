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
  --version=v1.2.1 \
  --path=/opt/kubesolo \
  --run-mode=service

# With proxy
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --proxy=http://proxy.company.com:8080
```

The installer writes the resolved settings to `/etc/kubesolo/config.yaml` and starts the service with `--config=/etc/kubesolo/config.yaml`. To change a setting later, edit that file (or use `kubesoloctl config set`) and restart, rather than reinstalling. See the [installer flags reference](docs/installation/flags.md) for every flag and the [configuration file reference](docs/configuration/config-file.md) for every setting.

Every download is checked against the release's published SHA-256 checksum before anything is installed, and the binary is run once to confirm it suits this host and is the version asked for. All of that, and generating the configuration, happens before a running KubeSolo is stopped: an install that fails — a wrong version, a bad setting, a corrupt archive — leaves the cluster that was there running.

#### Upgrading with the installer

Run the installer again with a newer version on a host where KubeSolo is already installed, and it upgrades it the way [`kubesoloctl upgrade`](docs/installation/kubesoloctl.md#upgrade--rollback--status) does: the datastore, binary and configuration are backed up, the new version opens a copy of the datastore first, and if it is not healthy within ten minutes the previous version is restored automatically.

```bash
curl -sfL https://get.kubesolo.io | sudo KUBESOLO_VERSION=v1.2.2 sh -
```

The existing `/etc/kubesolo/config.yaml` is kept as it is. Settings passed to an upgrade are not applied — the installer says so. Change settings with `kubesoloctl config set`, which validates them against the running KubeSolo. Running the installer again with the version already installed reinstalls the service, also keeping the configuration.

| Variable | Effect |
|---|---|
| `KUBESOLO_FORCE=true` | Allow installing a version older than the installed one. To undo the last upgrade, use `kubesoloctl rollback` instead. |
| `KUBESOLO_HEALTH_TIMEOUT=SECONDS` | How long the new version has to become healthy before it is rolled back (default 600, minimum 60). |
| `KUBESOLO_SHA256=SUM` | The expected checksum of the archive, instead of the published one. |

See the [upgrade API](docs/configuration/upgrade-api.md) for how the upgrade works and what is backed up.

The pre-flight checks stop the install if Docker is installed or running, if the hostname is not RFC 1123 compliant (lowercase only), if `iptables` or its comment module is missing, or if required cgroup controllers are not available.

**Automatic Detection:**
- **Init System**: systemd, SysV init, OpenRC, s6, runit, upstart
- **libc Type**: Automatically downloads glibc or musl binaries
- **Architecture**: amd64, arm64, arm, riscv64

**Supported Systems:**
- **glibc systems**: Ubuntu, CentOS, Debian, RHEL, SUSE, etc.
- **musl systems**: Alpine Linux, Void Linux (musl), embedded systems
- **Mixed environments**: Automatically selects correct binary variant

**Run Modes:**
- `service` (default): Creates proper service files for your init system, or falls back to `daemon` if no supported init system is detected
- `daemon`: Runs as background process with PID file (`/var/run/kubesolo.pid`, logs in `/var/log/kubesolo.log`)
- `foreground`: Runs in foreground (for testing/debugging)

### 2. Minimal Installer (`install-minimal.sh`)

**Best for:** Extremely constrained busybox-based systems

This script provides basic installation with minimal dependencies for the most constrained environments:

```bash
# Download and run
wget -O - https://raw.githubusercontent.com/portainer/kubesolo/develop/install-minimal.sh | sh

# Or with environment variables
KUBESOLO_VERSION=v1.2.1 KUBESOLO_PATH=/opt/kubesolo sh install-minimal.sh

# Alpine / musl systems: the minimal installer does not detect libc
sh install-minimal.sh --musl
```

**Features:**
- Works with busybox utilities only
- Creates simple init script
- Provides `kubesolo-ctl` management tool
- Minimal dependencies (only requires `tar` and `wget`/`curl`)

The minimal installer accepts only `--version`, `--path`, `--temp-dir`, `--musl`/`--glibc`, `--offline` and `--bin-path` (local binary or archive). It does not write `/etc/kubesolo/config.yaml` and only starts KubeSolo if you answer yes to its prompt; its init script runs `kubesolo --path=...`. KubeSolo still reads `/etc/kubesolo/config.yaml` if you create one, so use that for any other setting.

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

The universal installer (`install.sh`) reads these environment variables. The values shown are the defaults:

```bash
export KUBESOLO_VERSION="v1.2.1"                # Version to install
export KUBESOLO_PATH="/var/lib/kubesolo"        # Data directory
export KUBESOLO_APISERVER_EXTRA_SANS=""         # Extra API server certificate SANs
export KUBESOLO_PORTAINER_EDGE_ID=""            # Portainer Edge ID
export KUBESOLO_PORTAINER_EDGE_KEY=""           # Portainer Edge Key
export KUBESOLO_PORTAINER_EDGE_ASYNC="false"    # Async mode
export KUBESOLO_PORTAINER_EDGE_IMAGE="docker.io/portainer/agent:lts"  # Edge Agent image
export KUBESOLO_LOAD_BALANCER="true"            # Built-in load balancer
export KUBESOLO_LOCAL_STORAGE="true"            # Local-path storage provisioner
export KUBESOLO_LOCAL_STORAGE_SHARED_PATH=""    # Shared file system for local storage
export KUBESOLO_DB_WAL_REPAIR="false"           # SQLite WAL repair on startup
export KUBESOLO_DISABLE_IPV6="false"            # Disable IPv6
export KUBESOLO_STARTUP_TIMEOUT="600"           # Per-component startup timeout (seconds)
export KUBESOLO_CPU_MANAGER_POLICY="none"       # CPU manager policy (none or static)
export KUBESOLO_CPU_MANAGER_POLICY_OPTIONS=""   # Static policy options
export KUBESOLO_RESERVED_CPUS=""                # Cpuset reserved for the host
export KUBESOLO_SYSTEM_RESERVED=""              # Resources withheld from allocatable
export KUBESOLO_D2K="false"                     # d2k Docker API translator
export KUBESOLO_D2K_NAMESPACE="d2k"             # d2k namespace
export KUBESOLO_DEBUG="false"                   # Debug logging
export KUBESOLO_PPROF_SERVER="false"            # Enable pprof
export KUBESOLO_RUN_MODE="service"              # service, daemon or foreground
export KUBESOLO_PROXY=""                        # Corporate proxy for HTTP/HTTPS requests
export KUBESOLO_OFFLINE="false"                 # Download the offline build
export KUBESOLO_OFFLINE_INSTALL=""              # Install from a local archive or binary
export KUBESOLO_DOWNLOAD_DIR=""                 # Same as --download-only=DIR
export KUBESOLO_INSTALL_PREREQS="false"         # Install missing prerequisites (nftables on Alpine)
export KUBESOLO_SHA256=""                       # Expected archive checksum (default: the published one)
export KUBESOLO_FORCE="false"                   # Allow installing an older version over a newer one
export KUBESOLO_HEALTH_TIMEOUT="600"            # Seconds an upgrade has to become healthy
```

Any other `KUBESOLO_*` variable that KubeSolo itself recognises, such as `KUBESOLO_MTU`, `KUBESOLO_NODE_IP` or `KUBESOLO_METRICS_SERVER`, is also written into `/etc/kubesolo/config.yaml` when it is set during the install. See [Settings without an installer flag](docs/installation/flags.md#settings-without-an-installer-flag).

`sudo` drops environment variables by default. Use `sudo -E`, or pass the equivalent flag instead:

```bash
curl -sfL https://get.kubesolo.io | KUBESOLO_DEBUG=true sudo -E sh -
curl -sfL https://get.kubesolo.io | sudo sh -s -- --debug=true
```

The minimal installer reads only `KUBESOLO_VERSION`, `KUBESOLO_PATH`, `KUBESOLO_OFFLINE`, `KUBESOLO_BIN_PATH`, `USE_MUSL` and `TEMP_DIR`.

## Industrial Device Considerations

### 1. **Read-Only Root Filesystem**

Many industrial devices use read-only root filesystems. Consider:

```bash
# Install to writable partition
curl -sfL https://get.kubesolo.io | sudo sh -s -- --path=/data/kubesolo
```

The data directory holds the cluster database, certificates and container state, so it should be on persistent storage. The installer also writes `/usr/local/bin/kubesolo` and `/etc/kubesolo/config.yaml`; both locations must be writable at install time.

### 2. **Limited Storage**

For devices with limited storage:

```bash
# Use minimal installer
./install-minimal.sh

# Disable the local storage provisioner (on by default)
curl -sfL https://get.kubesolo.io | sudo sh -s -- --local-storage=false
```

### 3. **No Internet Access**

For air-gapped installations, use the offline build. It embeds every image KubeSolo deploys itself, so nothing is pulled at startup. The one exception is a custom Portainer Edge Agent image (`portainer.image`): only the default agent image is embedded, so a custom one is always pulled from its registry. The default (online) build still needs registry access when KubeSolo starts.

```bash
# On a connected Linux machine with the same architecture and libc as the target
curl -sfL https://get.kubesolo.io | sh -s -- --offline --download-only=./kubesolo-bundle

# Copy ./kubesolo-bundle to the target, then on the target
cd kubesolo-bundle
sudo sh install.sh --offline-install=<downloaded archive>
```

Copy the `SHA256SUMS` file `--download-only` writes along with the archive: the install checks the archive against it. An archive with no `SHA256SUMS` beside it still installs, with a warning that it could not be verified.

Pass the archive that `--download-only` saved. Its name is `kubesolo-<version>-linux-<arch>[-musl]-offline.tar.gz`, for example `kubesolo-v1.2.1-linux-amd64-offline.tar.gz`. The bundle is built for the OS, architecture and libc of the machine that downloads it, so download on Linux (not macOS), and on a glibc machine for a glibc target or a musl machine (such as Alpine) for a musl target. If you can't, download the matching archive from the [releases page](https://github.com/portainer/kubesolo/releases) instead.

`--download-only` does not need root and does not run the pre-flight checks. See [--download-only](docs/installation/flags.md#--download-only) for details. The archives are also on the [releases page](https://github.com/portainer/kubesolo/releases) (`kubesolo-<version>-linux-<arch>[-musl]-offline.tar.gz`).

### 4. **Corporate Proxy Support**

For environments requiring corporate proxy access:

```bash
# Using command-line flag
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --proxy=http://proxy.company.com:8080

# Using environment variable (sudo -E keeps it)
curl -sfL https://get.kubesolo.io | KUBESOLO_PROXY="http://proxy.company.com:8080" sudo -E sh -

# Combined with other options
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --proxy=http://proxy.company.com:8080 \
  --version=v1.2.1 \
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
curl -sfL https://get.kubesolo.io | sudo sh -s -- --run-mode=daemon

# Or run manually, after an install has written the configuration file
/usr/local/bin/kubesolo --config=/etc/kubesolo/config.yaml > /var/log/kubesolo.log 2>&1 &
echo $! > /var/run/kubesolo.pid
```

Daemon mode does not survive a reboot; your init system needs to start KubeSolo again.

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
- `armv7l` (arm) - Alpine Linux ARM 32-bit
- `riscv64` - Alpine Linux RISC-V 64-bit

**Note:** The installer automatically detects your system type and downloads the appropriate binary. musl binaries are static and work on any musl-based system without additional dependencies.

## Troubleshooting

### Init System Detection Issues

The installer prints the detected init system near the start of its output (`Detected init system: ...`). If it picks the wrong one, or none:

```bash
# Force daemon mode
curl -sfL https://get.kubesolo.io | sudo sh -s -- --run-mode=daemon
```

### Service Not Starting

```bash
# Check logs
journalctl -u kubesolo -f                       # systemd
tail -f /var/log/kubesolo.log                   # daemon mode, s6, runit

# Check the configuration KubeSolo will start with
sudo kubesolo --config=/etc/kubesolo/config.yaml --print-config

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

If `kubectl` is installed when you run the installer, it merges the KubeSolo context into `~/.kube/config` for you (the invoking user's, under `sudo`) and backs up the previous file.

## Uninstalling

`uninstall.sh` stops KubeSolo — and any upgrade in progress, and the pods' own processes, which outlive the service — and removes the binary, service files, CNI configuration and `/etc/kubesolo/config.yaml`:

```bash
curl -sfL https://raw.githubusercontent.com/portainer/kubesolo/develop/uninstall.sh | sudo sh -s --
```

| Flag | Effect |
|---|---|
| `--path=PATH` | Data directory, if not `/var/lib/kubesolo` |
| `--remove-data` | Also delete the data directory (cluster database, certificates, images) |
| `--remove-kubeconfig` | Also remove the KubeSolo entries from `~/.kube/config` |
| `--keep-config` | Leave `/etc/kubesolo/config.yaml` in place for a later reinstall |

`kubesoloctl uninstall` does the same; see the [kubesoloctl guide](docs/installation/kubesoloctl.md).

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
curl -sfL https://get.kubesolo.io | sh -s -- --install-prereqs

# The installer will:
# 1. Detect OpenRC init system
# 2. Detect musl libc and download musl-compatible binary
# 3. Install nftables if missing (kube-proxy needs it on Alpine)
# 4. Create appropriate OpenRC service file
```

Without `--install-prereqs` the installer stops if `nft` is missing; install it yourself with `apk add nftables`.

**Alpine-specific features:**
- Automatically downloads musl-compatible static binary
- Creates OpenRC service configuration
- Works on x86_64, aarch64, armv7l and riscv64 Alpine systems
- Needs `nftables`, which `--install-prereqs` installs for you

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