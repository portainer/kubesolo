# Install Script Flags

The KubeSolo install script accepts flags that control version selection, runtime behaviour, Portainer integration, and offline workflows. Every flag has a corresponding environment variable, which is useful when piping the script directly from a URL.

The script only accepts the flags listed on this page, plus `--help`. Any other argument, such as `--mtu=1400`, is silently ignored. Settings without an installer flag can still be set at install time through their environment variable, or after install through the configuration file. See [Settings without an installer flag](#settings-without-an-installer-flag).

> **These flags configure the installer.** From KubeSolo v1.2.1 the installer
> records them in a configuration file at `/etc/kubesolo/config.yaml`, and the
> installed service reads its settings from there rather than from a command line.
>
> Use these flags to install. To change a setting **afterwards**, edit the
> configuration file — see [Configuration file](../configuration/config-file.md) —
> rather than reinstalling.

### How the configuration file is written

The installer does not write the file itself. It runs the newly installed binary with the resolved settings plus `--print-config`, saves the output as `/etc/kubesolo/config.yaml` (mode `0600`), and starts the service with `--config=/etc/kubesolo/config.yaml` as its only flag. The binary resolves settings in its usual order, so the file reflects:

1. the built-in defaults,
2. an existing `/etc/kubesolo/config.yaml`, if there is one (a reinstall keeps its settings),
3. every `KUBESOLO_*` environment variable in the installer's environment that KubeSolo recognises, including ones the installer has no flag for, such as `KUBESOLO_MTU` or `KUBESOLO_NODE_IP` ([full list](../configuration/config-file.md#flag-and-environment-variable-equivalents)),
4. the installer flags.

Two consequences of that order:

- A boolean the installer only passes when it is `true` (`--debug`, `--pprof-server`, `--portainer-edge-async`, `--d2k`) cannot switch off a value an existing file has set to `true`. Use `kubesoloctl config set` for that.
- Environment variables only reach the file if they reach the script. `sudo` drops them unless you use `sudo -E` (the installer also tries to recover `KUBESOLO_*` variables from the parent process when `KUBESOLO_PORTAINER_EDGE_KEY` is missing).

If the binary predates the configuration file, or `--print-config` fails, the installer falls back to passing flags on the service command line, and only the settings the installer itself knows about are applied.

---

## Install channels

Two URLs serve the install script depending on which channel you need.

| URL | Source | Intended use |
|---|---|---|
| `https://get.kubesolo.io` | Latest release tag | Production installs |
| `https://get-dev.kubesolo.io` | `develop` branch | Testing unreleased changes |

`get.kubesolo.io` always serves the script from the most recent stable release tag. `get-dev.kubesolo.io` always serves the script directly from the `develop` branch and may include features or changes that have not yet been released.

> **Warning:** `get-dev.kubesolo.io` should not be used in production. It may install pre-release behaviour or defaults that differ from the stable release.

Flags are passed identically to both URLs. When piping the script, use `sh -s --` so the shell reads from stdin (`-s`) and treats everything after `--` as arguments to the script rather than options to `sh` itself:

```bash
# Stable
curl -sfL https://get.kubesolo.io | sudo sh -s -- --version=v1.2.1 --debug=true

# Development
curl -sfL https://get-dev.kubesolo.io | sudo sh -s -- --debug=true
```

When running a downloaded copy of the script directly, flags are passed normally:

```bash
sudo sh install.sh --version=v1.2.1 --debug=true
```

---

## Flags

### --version

Set the KubeSolo version to install. Defaults to the version the script was released with.

| Flag | Env var | Default |
|---|---|---|
| `--version=VERSION` | `KUBESOLO_VERSION` | `v1.2.1` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --version=v1.2.1
```

---

### --path

Override the directory KubeSolo uses for its data, PKI, and configuration. Useful when the default location does not have sufficient disk space.

| Flag | Env var | Default |
|---|---|---|
| `--path=PATH` | `KUBESOLO_PATH` | `/var/lib/kubesolo` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --path=/data/kubesolo
```

---

### --apiserver-extra-sans

Add extra Subject Alternative Names to the API server TLS certificate. Accepts a comma-separated list of IPs or DNS names. Required when `kubectl` will connect to the API server through a load balancer address or hostname that is not already covered by the certificate.

| Flag | Env var | Default |
|---|---|---|
| `--apiserver-extra-sans=SANS` | `KUBESOLO_APISERVER_EXTRA_SANS` | _(none)_ |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --apiserver-extra-sans=10.0.0.5,k8s.corp.internal
```

---

### --cpu-manager-policy, --cpu-manager-policy-options, --reserved-cpus and --system-reserved

Give latency-sensitive workloads exclusive CPU cores instead of letting every pod share all of them. Set `--cpu-manager-policy=static` and pods that request whole CPUs under the Guaranteed QoS class get cores no other pod may run on. Useful for audio processing, motion control, machine vision and similar workloads that must not be preempted by neighbours.

`--reserved-cpus` is the cpuset held back for the host and KubeSolo itself, and is never handed out as an exclusive core. The static policy requires a non-empty reservation, so it defaults to CPU `0`.

| Flag | Env var | Default |
|---|---|---|
| `--cpu-manager-policy=POLICY` | `KUBESOLO_CPU_MANAGER_POLICY` | `none` |
| `--cpu-manager-policy-options=OPTS` | `KUBESOLO_CPU_MANAGER_POLICY_OPTIONS` | _(empty)_ |
| `--reserved-cpus=CPUSET` | `KUBESOLO_RESERVED_CPUS` | `0` when the static policy is used |
| `--system-reserved=LIST` | `KUBESOLO_SYSTEM_RESERVED` | _(empty)_ |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --cpu-manager-policy=static --reserved-cpus=0
```

`--system-reserved` takes quantities instead of CPU indexes (`cpu=1,memory=500Mi`, also `ephemeral-storage` and `pid`). Its `cpu=` entry can stand in for `--reserved-cpus`, but the kubelet then chooses which cores to hold back, so host-level isolation cannot be aligned to them. Setting both is allowed: the cpuset wins, per upstream precedence, and KubeSolo warns that the `cpu=` entry is ignored.

CPU pinning is not supported in container mode. See the [CPU pinning guide](../configuration/cpu-pinning.md) for the pod requirements, how to verify pinning took effect, and the host-level tuning that deterministic latency also needs.

---

### --portainer-edge-id and --portainer-edge-key

Connect KubeSolo to a Portainer server as a Portainer Edge Agent. Both flags must be supplied together.

| Flag | Env var | Default |
|---|---|---|
| `--portainer-edge-id=ID` | `KUBESOLO_PORTAINER_EDGE_ID` | _(none)_ |
| `--portainer-edge-key=KEY` | `KUBESOLO_PORTAINER_EDGE_KEY` | _(none)_ |

Because the Edge Key contains special characters, use the environment variable form when piping the script:

```bash
curl -sfL https://get.kubesolo.io | \
  KUBESOLO_PORTAINER_EDGE_ID=<your-edge-id> \
  KUBESOLO_PORTAINER_EDGE_KEY=<your-edge-key> \
  sudo -E sh
```

---

### --portainer-edge-async

Enable asynchronous mode for the Portainer Edge Agent. In async mode the agent does not maintain a persistent tunnel to the Portainer server; it polls instead.

| Flag | Env var | Default |
|---|---|---|
| `--portainer-edge-async=true\|false` | `KUBESOLO_PORTAINER_EDGE_ASYNC` | `false` |

```bash
curl -sfL https://get.kubesolo.io | \
  KUBESOLO_PORTAINER_EDGE_ID=<your-edge-id> \
  KUBESOLO_PORTAINER_EDGE_KEY=<your-edge-key> \
  sudo -E sh -s -- --portainer-edge-async=true
```

---

### --portainer-edge-image

Full image reference deployed for the Portainer Edge Agent, including the tag. Accepts any registry, repository, and tag. Only the default image is loaded from the bundled image; any other reference is pulled from the registry, so the node needs access to it. If the registry cannot be reached at startup, KubeSolo logs a warning and continues — the kubelet retries the pull when the agent pod starts.

Short references are expanded the way Docker expands them, so `portainerci/agent:develop` becomes `docker.io/portainerci/agent:develop` and an omitted tag defaults to `latest`. Use a full host prefix for other registries, e.g. `ghcr.io/portainer/agent:2.34.0`.

Note that the deployment is only created once — on reboot it is restored from the database. Changing this flag on an existing installation does not update an already deployed agent.

| Flag | Env var | Default |
|---|---|---|
| `--portainer-edge-image=IMAGE` | `KUBESOLO_PORTAINER_EDGE_IMAGE` | `docker.io/portainer/agent:lts` |

```bash
curl -sfL https://get.kubesolo.io | \
  KUBESOLO_PORTAINER_EDGE_ID=<your-edge-id> \
  KUBESOLO_PORTAINER_EDGE_KEY=<your-edge-key> \
  sudo -E sh -s -- --portainer-edge-image=docker.io/portainer/agent:sts
```

---

### --local-storage

Enable the [Local Path Provisioner](https://github.com/rancher/local-path-provisioner), which creates a `local-path` StorageClass backed by host-local directories. Workloads that request persistent volumes will have them provisioned automatically under the KubeSolo data path.

It is on by default. Pass `--local-storage=false` to install without it.

| Flag | Env var | Default |
|---|---|---|
| `--local-storage=true\|false` | `KUBESOLO_LOCAL_STORAGE` | `true` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --local-storage=false
```

To keep provisioned volumes on a shared file system, set `KUBESOLO_LOCAL_STORAGE_SHARED_PATH` (there is no installer flag for it).

---

### --d2k

Embed [d2k](https://github.com/portainer/d2k), the Portainer Docker-to-Kubernetes API translator, into the KubeSolo node. With `--d2k` set, KubeSolo deploys d2k into the namespace given by [`--d2k-namespace`](#--d2k-namespace), generates mTLS material under `/var/lib/kubesolo/pki/d2k/`, and exposes a Docker-compatible API endpoint on port `2376` via a LoadBalancer Service so existing Docker tooling can target the node without a separate translator deployment.

See [docs/configuration/d2k.md](../configuration/d2k.md) for the full integration guide.

| Flag | Env var | Default |
|---|---|---|
| `--d2k=true\|false` | `KUBESOLO_D2K` | `false` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --d2k=true
```

> **Architecture support:** The d2k container image is published only for `linux/amd64` and `linux/arm64`. On `arm` and `riscv64` builds, passing `--d2k` logs a warning at startup and the flag is silently cleared — no PKI material is generated, no image is imported, and no Kubernetes resources are created.

> **Load-balancer requirement:** d2k requires the built-in load balancer (`network.loadBalancer.enabled`, on by default). KubeSolo exits at startup if d2k is enabled with the load balancer turned off. The installer has no flag for the load balancer; it can only be turned off with `KUBESOLO_LOAD_BALANCER=false`.

> **Namespace is fixed after first start:** the d2k server certificate SANs are generated for the namespace set at first start. Changing `--d2k-namespace` on a later restart reuses the existing certificate with mismatched SANs. Delete `/var/lib/kubesolo/pki/d2k/server.crt` and `server.key` before restarting to regenerate the certificate for the new namespace.

---

### --d2k-namespace

Set the single Kubernetes namespace into which d2k is deployed and against which it translates Docker API calls. Only honoured when [`--d2k`](#--d2k) is set.

| Flag | Env var | Default |
|---|---|---|
| `--d2k-namespace=NAMESPACE` | `KUBESOLO_D2K_NAMESPACE` | `d2k` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --d2k=true --d2k-namespace=workloads
```

> **Namespace is fixed after first start:** changing this value on a later restart reuses the existing server certificate, whose SANs were generated for the original namespace. Delete `/var/lib/kubesolo/pki/d2k/server.crt` and `server.key` before restarting to regenerate the certificate for the new namespace.

---

### --debug

Enable verbose debug logging in the KubeSolo process.

| Flag | Env var | Default |
|---|---|---|
| `--debug=true\|false` | `KUBESOLO_DEBUG` | `false` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --debug=true
```

---

### --pprof-server

Start the Go pprof HTTP server on port `6060`. Intended for profiling and performance analysis.

| Flag | Env var | Default |
|---|---|---|
| `--pprof-server=true\|false` | `KUBESOLO_PPROF_SERVER` | `false` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --pprof-server=true
```

---

### --run-mode

Control how KubeSolo is started after installation.

| Mode | Behaviour |
|---|---|
| `service` | Registers and starts KubeSolo with the detected init system (systemd, OpenRC, runit, s6, Upstart, SysVinit). This is the recommended mode for persistent installations. |
| `daemon` | Starts KubeSolo as a background process with a PID file at `/var/run/kubesolo.pid` and logs at `/var/log/kubesolo.log`. Useful when no supported init system is available. `service` mode also falls back to this when it cannot detect an init system. |
| `foreground` | Runs KubeSolo in the foreground of the current shell. Primarily for debugging. |

| Flag | Env var | Default |
|---|---|---|
| `--run-mode=MODE` | `KUBESOLO_RUN_MODE` | `service` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --run-mode=daemon
```

---

### --proxy

Route KubeSolo's outbound HTTP and HTTPS traffic through a proxy. The value is applied as `HTTP_PROXY` and `HTTPS_PROXY` in the service environment; `NO_PROXY` is automatically set to `localhost,127.0.0.1`.

| Flag | Env var | Default |
|---|---|---|
| `--proxy=URL` | `KUBESOLO_PROXY` | _(none)_ |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --proxy=http://proxy.corp.internal:3128
```

---

### --offline-install

Install from a local binary or archive instead of downloading from GitHub. Accepts a path to a `.tar.gz` archive, a `.zip` archive, or a raw binary. Useful in air-gapped environments.

| Flag | Env var | Default |
|---|---|---|
| `--offline-install=PATH` | `KUBESOLO_OFFLINE_INSTALL` | _(none)_ |

```bash
sudo sh install.sh --offline-install=./kubesolo-v1.2.1-linux-amd64.tar.gz
```

See [--download-only](#--download-only) for how to prepare the required files on an internet-connected machine.

---

### --offline

Download the offline build instead of the default online one. The offline build embeds every container image KubeSolo needs, so the node does not pull anything at startup. Use it for air-gapped machines, and combine it with `--download-only` when preparing a bundle.

| Flag | Env var | Default |
|---|---|---|
| `--offline` | `KUBESOLO_OFFLINE=true` | _(off)_ |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --offline
```

---

### --install-prereqs

Install missing prerequisites instead of failing. Today this covers `nftables` on Alpine, which kube-proxy needs there.

| Flag | Env var | Default |
|---|---|---|
| `--install-prereqs` | `KUBESOLO_INSTALL_PREREQS=true` | _(off)_ |

```bash
curl -sfL https://get.kubesolo.io | sh -s -- --install-prereqs
```

---

### --download-only

Download the KubeSolo binary archive and a copy of the install script to a local directory, then exit. No installation is performed and no pre-flight checks are run. **Root is not required** — this flag is safe to run on a developer laptop (including one with Docker installed) and is intended for preparing an offline bundle before transferring it to an air-gapped target.

Accepts an optional directory path. Defaults to the current directory when no path is given.

| Flag | Env var | Default |
|---|---|---|
| `--download-only[=DIR]` | `KUBESOLO_DOWNLOAD_DIR` | `.` (current directory) |

The downloaded files are named after the detected OS, architecture, libc, version and variant:

```
<DIR>/
  install.sh
  kubesolo-<version>-linux-<arch>[-musl][-offline].tar.gz
```

The copy of `install.sh` always comes from `get.kubesolo.io`.

Without `--offline` (or `KUBESOLO_OFFLINE=true`) this downloads the online build, which still pulls container images at startup. For a machine with no internet access, add `--offline`.

**Download the offline build to the current directory:**

```bash
curl -sfL https://get.kubesolo.io | sh -s -- --offline --download-only
```

**Download to a specific directory:**

```bash
curl -sfL https://get.kubesolo.io | sh -s -- --offline --download-only=./kubesolo-offline
```

**Install on the air-gapped machine:**

```bash
sudo sh install.sh --offline-install=./kubesolo-v1.2.1-linux-amd64-offline.tar.gz
```

> **Note:** The archive is downloaded for the architecture of the machine running `--download-only`. If the target machine has a different architecture, pass `--version` alongside `--download-only` but run the download step on a machine matching the target architecture, or obtain the correct archive directly from the [GitHub releases page](https://github.com/portainer/kubesolo/releases).

---

## Settings without an installer flag

The installer has flags for the settings above only. Everything else KubeSolo can be configured with, including `network.mtu`, `network.nodeIP`, `network.loadBalancer.ip`, `metrics.enabled`, `runtime.endpoint` and `kubernetes.nodeName`, can be set in either of two ways.

**At install time, through the environment.** Every `KUBESOLO_*` variable KubeSolo recognises is picked up when the installer writes the configuration file (see [How the configuration file is written](#how-the-configuration-file-is-written)). Keep it through `sudo` with `-E`:

```bash
curl -sfL https://get.kubesolo.io | \
  KUBESOLO_MTU=1400 \
  KUBESOLO_NODE_IP=10.0.0.5 \
  KUBESOLO_METRICS_SERVER=true \
  sudo -E sh -
```

A few of these the installer also reads itself, so they are applied even on the flag-based fallback path:

| Env var | Setting | Installer default |
|---|---|---|
| `KUBESOLO_LOAD_BALANCER` | `network.loadBalancer.enabled` | `true` |
| `KUBESOLO_LOCAL_STORAGE_SHARED_PATH` | `storage.localPath.sharedPath` | _(none)_ |
| `KUBESOLO_DB_WAL_REPAIR` | `storage.dbWALRepair` | `false` |
| `KUBESOLO_DISABLE_IPV6` | `network.disableIPv6` | `false` |
| `KUBESOLO_STARTUP_TIMEOUT` | `kubernetes.apiServer.startupTimeoutSeconds` | `600` |

The full mapping of variables to settings is in the [configuration file reference](../configuration/config-file.md#flag-and-environment-variable-equivalents).

**After install, through the configuration file.** Change the setting and restart:

```bash
sudo kubesoloctl config set network.mtu 1400
sudo kubesoloctl config set metrics.enabled true
sudo systemctl restart kubesolo
```

or edit `/etc/kubesolo/config.yaml` directly. See [Configuration file](../configuration/config-file.md).
