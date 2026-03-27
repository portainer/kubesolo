# Install Script Flags

The KubeSolo install script accepts flags that control version selection, runtime behaviour, Portainer integration, and offline workflows. Every flag has a corresponding environment variable, which is useful when piping the script directly from a URL.

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
curl -sfL https://get.kubesolo.io | sudo sh -s -- --version=v1.1.2 --local-storage=true

# Development
curl -sfL https://get-dev.kubesolo.io | sudo sh -s -- --local-storage=true
```

When running a downloaded copy of the script directly, flags are passed normally:

```bash
sudo sh install.sh --version=v1.1.2 --local-storage=true
```

---

## Flags

### --version

Set the KubeSolo version to install. Defaults to the latest stable release bundled with the script.

| Flag | Env var | Default |
|---|---|---|
| `--version=VERSION` | `KUBESOLO_VERSION` | `v1.1.2` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --version=v1.1.2
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

### --local-storage

Enable the [Local Path Provisioner](https://github.com/rancher/local-path-provisioner), which creates a `local-path` StorageClass backed by host-local directories. Workloads that request persistent volumes will have them provisioned automatically under the KubeSolo data path.

| Flag | Env var | Default |
|---|---|---|
| `--local-storage=true\|false` | `KUBESOLO_LOCAL_STORAGE` | `false` |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --local-storage=true
```

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
| `daemon` | Starts KubeSolo as a background process with a PID file at `/var/run/kubesolo.pid` and logs at `/var/log/kubesolo.log`. Useful when no supported init system is available. |
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
sudo sh install.sh --offline-install=./kubesolo-v1.1.2-linux-amd64.tar.gz
```

See [--download-only](#--download-only) for how to prepare the required files on an internet-connected machine.

---

### --download-only

Download the KubeSolo binary archive and a copy of the install script to a local directory, then exit. No installation is performed and no pre-flight checks are run. **Root is not required** — this flag is safe to run on a developer laptop (including one with Docker installed) and is intended for preparing an offline bundle before transferring it to an air-gapped target.

Accepts an optional directory path. Defaults to the current directory when no path is given.

| Flag | Env var | Default |
|---|---|---|
| `--download-only[=DIR]` | `KUBESOLO_DOWNLOAD_DIR` | `.` (current directory) |

The downloaded files are named after the detected OS, architecture, and version:

```
<DIR>/
  install.sh
  kubesolo-<version>-linux-<arch>.tar.gz
```

**Download to the current directory:**

```bash
curl -sfL https://get.kubesolo.io | sh -s -- --download-only
```

**Download to a specific directory:**

```bash
curl -sfL https://get.kubesolo.io | sh -s -- --download-only=./kubesolo-offline
```

**Install on the air-gapped machine:**

```bash
sudo sh install.sh --offline-install=./kubesolo-v1.1.2-linux-amd64.tar.gz
```

> **Note:** The archive is downloaded for the architecture of the machine running `--download-only`. If the target machine has a different architecture, pass `--version` alongside `--download-only` but run the download step on a machine matching the target architecture, or obtain the correct archive directly from the [GitHub releases page](https://github.com/portainer/kubesolo/releases).
