# kubesoloctl (CLI)

`kubesoloctl` is a single, dependency-free binary that installs, manages, and upgrades KubeSolo on a host. It wraps the whole lifecycle — pre-flight checks, download, service setup, kubeconfig wiring, upgrades, reset, and uninstall — behind a small set of commands, and it works in two fundamentally different ways:

- **Host mode** (Linux): installs the KubeSolo binary and runs it directly as a system service (systemd, OpenRC, SysV, …). This is the production path for edge devices and gateways.
- **Container mode**: runs KubeSolo inside a container. Useful for **development and CI** on macOS, Windows (WSL2), or any Linux host that has a container engine. On macOS this is the only supported mode, because the KubeSolo binary is Linux-only.

> `kubesoloctl` is complementary to the `curl -sfL https://get.kubesolo.io | sh` installer. The shell installer is the quickest path for a Linux host; `kubesoloctl` is the cross-platform tool and the way to run KubeSolo in a container for local development.

---

## Getting kubesoloctl

Download the binary for your platform from the [GitHub releases page](https://github.com/portainer/kubesolo/releases) and put it on your `PATH`:

```bash
# Example: Linux amd64 — adjust os/arch to match your machine
curl -sfL -o kubesoloctl \
  https://github.com/portainer/kubesolo/releases/latest/download/kubesoloctl-linux-amd64
chmod +x kubesoloctl
sudo mv kubesoloctl /usr/local/bin/
```

Released assets follow the pattern `kubesoloctl-<os>-<arch>` (e.g. `kubesoloctl-linux-arm64`, `kubesoloctl-darwin-arm64`). Published targets are `linux/amd64`, `linux/arm64`, `linux/arm`, `linux/riscv64`, `darwin/amd64` and `darwin/arm64`. The `linux/arm` build is compiled for ARMv7 — ARMv6 boards (Pi 1, original Pi Zero) are not supported.

---

## Run modes

| Mode | Flag | Where it runs | Typical use |
|---|---|---|---|
| `service` | _(default on Linux)_ | KubeSolo binary as a system service | Production / edge |
| `container` | `--run-mode=container` | KubeSolo inside a container | Dev / CI (macOS, WSL2, Linux) |
| `daemon` | `--run-mode=daemon` | Background process, no init system | Minimal hosts |
| `foreground` | `--run-mode=foreground` | Stays in the terminal | Debugging |

On **macOS**, the mode is forced to `container` automatically.

---

## Quickstart

### Linux host (service mode)

```bash
sudo kubesoloctl install
```

This runs pre-flight checks, installs the binary to `/usr/local/bin/kubesolo`, configures and starts the service, and merges the admin kubeconfig into your `~/.kube/config`. Then:

```bash
kubectl get nodes --watch    # wait until STATUS: Ready
kubectl get pods -A
```

### Container mode (macOS, WSL2, or Linux dev)

Requires a running container engine (Docker Engine / Docker Desktop).

```bash
# On macOS this is implied; elsewhere request it explicitly:
kubesoloctl install --run-mode=container
```

KubeSolo starts in a container, the API server is published on a random localhost port, and your kubeconfig is merged and pointed at it automatically. Container mode also adjusts cgroups, mounts, DNS, and eviction thresholds so the node comes up cleanly inside a container.

```bash
kubectl get nodes --watch
```

If the host port ever changes (after an upgrade or reset), refresh your kubeconfig:

```bash
kubesoloctl kubeconfig fetch
```

---

## Commands

| Command | Purpose |
|---|---|
| `install` | Install KubeSolo and configure the service / start the container |
| `check` | Run pre-flight checks only, without installing |
| `upgrade --version=<v>` | Upgrade to a newer version, with a backup and automatic rollback |
| `rollback` | Restore the version, datastore and configuration from before the last upgrade |
| `status` | Show the running version, health, and the last upgrade or rollback |
| `reset` | Wipe all cluster state and start fresh (keeps the install) |
| `uninstall` | Stop and remove KubeSolo, service files, and (optionally) data |
| `kubeconfig` | Print, write, fetch, or view the admin kubeconfig |
| `d2k fetch` | Set up a Docker context for the d2k endpoint (container mode) |
| `download` | Build an offline install bundle for an air-gapped machine |
| `version` | Print version information |

### install

Common flags:

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--version` | `KUBESOLO_VERSION` | matched to the CLI build | KubeSolo version to install |
| `--path` | `KUBESOLO_PATH` | `/var/lib/kubesolo` | Data directory |
| `--run-mode` | `KUBESOLO_RUN_MODE` | `service` | `service`, `daemon`, `foreground`, or `container` |
| `--name` | `KUBESOLO_NAME` | `kubesolo` | Instance name (container name + kubeconfig context) |
| `--image` | `KUBESOLO_IMAGE` | `portainer/kubesolo:<version>` | Container image (container mode) |
| `--container-ports` | `KUBESOLO_CONTAINER_PORTS` | _(none)_ | Workload host ports to publish (container mode) |
| `--d2k` | `KUBESOLO_D2K` | `false` | Enable the Docker-compatible API translator |
| `--local-storage` | `KUBESOLO_LOCAL_STORAGE` | on (see below) | Enable the local-path storage provisioner |
| `--node-ip` | `KUBESOLO_NODE_IP` | _(auto-detect)_ | Override the detected node IP |
| `--mtu` | `KUBESOLO_MTU` | _(auto-detect)_ | Override the detected MTU; in container mode also sizes the outer Docker network |
| `--portainer-edge-image` | `KUBESOLO_PORTAINER_EDGE_IMAGE` | `docker.io/portainer/agent:lts` | Image deployed for the Portainer edge agent |
| `--offline-install` | `KUBESOLO_OFFLINE_INSTALL` | _(none)_ | Install from a local tarball/binary instead of downloading |
| `--proxy` | `KUBESOLO_PROXY` | _(none)_ | HTTP/HTTPS proxy injected into the service environment |
| `--cpu-manager-policy` | `KUBESOLO_CPU_MANAGER_POLICY` | `none` | `static` gives Guaranteed-QoS pods exclusive cores (not supported in container mode) |
| `--cpu-manager-policy-options` | `KUBESOLO_CPU_MANAGER_POLICY_OPTIONS` | _(none)_ | Comma-separated `key=value` options for the static policy |
| `--reserved-cpus` | `KUBESOLO_RESERVED_CPUS` | `0` when the static policy is used | Cpuset reserved for the host and KubeSolo itself |
| `--system-reserved` | `KUBESOLO_SYSTEM_RESERVED` | _(none)_ | Resources withheld from allocatable, e.g. `cpu=1,memory=500Mi` |

`--local-storage` can only turn the provisioner on, and it is already on by default in KubeSolo, so the local-path provisioner is installed either way. `--local-storage=false` does not disable it. On a host install, disable it afterwards with `sudo kubesoloctl config set storage.localPath.enabled false` and restart KubeSolo. That only stops it being deployed on later starts; the existing provisioner keeps running until you remove it with `kubectl delete namespace local-path-storage` and `kubectl delete storageclass local-path`.

On a host install of KubeSolo v1.2.1 or later, `install` writes the settings to `/etc/kubesolo/config.yaml` and the service runs with `--config` only. Unlike the shell installer, it does not fold `KUBESOLO_*` variables for settings it has no flag for into that file. Container mode keeps passing flags to the container. See [Configuration file](../configuration/config-file.md).

CPU pinning flags are validated before anything is installed, so a bad cpuset or an unsupported policy option fails immediately rather than crash-looping the service. See the [CPU pinning guide](../configuration/cpu-pinning.md).

### kubeconfig

```bash
kubesoloctl kubeconfig            # print the admin kubeconfig (host mode)
kubesoloctl kubeconfig view       # print it (reads from the container in container mode)
kubesoloctl kubeconfig fetch      # merge it into ~/.kube/config
```

`fetch` reads from the container when one is running, otherwise from the local data directory.

### upgrade / rollback / status

```bash
sudo kubesoloctl upgrade --version=v1.2.2      # verified, backed up, rolled back automatically on failure
sudo kubesoloctl upgrade --version=v1.2.2 --offline-install=/tmp/bundle/kubesolo-v1.2.2-linux-amd64.tar.gz
sudo kubesoloctl rollback                      # undo the last upgrade (loses cluster changes made since)
sudo kubesoloctl status                        # version, health, last run, rollback target
```

On a host install, `upgrade` checks everything it can while KubeSolo keeps
running — the release checksum, the binary's architecture and C library, disk
space — backs up the datastore, binary and configuration file, and has the new
version open a copy of the datastore. Only then does it stop KubeSolo and switch
over. If the new version is not healthy within `--health-timeout` (default
10m, minimum 1m), the previous binary, datastore and configuration are restored. A failure
before the switch-over changes nothing at all.

Checksums come from the release's `SHA256SUMS` or, for older GitHub releases, the
digest GitHub publishes; pass `--sha256` to give one yourself. An
`--offline-install` archive needs a `SHA256SUMS` beside it, which `kubesoloctl
download` writes. Downgrades need `--force`; to undo the last upgrade, use
`rollback`.

When KubeSolo's [API](../configuration/upgrade-api.md) is enabled, kubesoloctl
asks KubeSolo to upgrade itself, so it cannot collide with an upgrade Portainer
started. Otherwise it runs the same upgrade directly. See the
[upgrade API](../configuration/upgrade-api.md) for how it works and what
`status` reports.

### reset / uninstall

```bash
kubesoloctl reset                      # wipe cluster state, keep the install (--force to skip the prompt)
kubesoloctl uninstall                  # remove KubeSolo; --purge also deletes data; container mode also cleans kubeconfig + Docker context
```

These commands detect automatically whether the instance runs as a container or a host service and act accordingly.

In container mode, `upgrade` pulls the new image before touching the running
container, and the replacement keeps the container's hostname, which is the
node's name.

---

## Container mode details

### Publishing workload ports

In container mode KubeSolo runs in its own network namespace, so a NodePort/LoadBalancer service or `hostPort` pod is only reachable if the port is published to the host — the same model as Kind's `extraPortMappings`. Use `--container-ports`:

```bash
kubesoloctl install --run-mode=container --container-ports=9001,8080:80,9000-9100,53/udp
```

| Entry | Meaning |
|---|---|
| `9001` | host `9001` → container `9001` |
| `9000-9100` | range, host==container (1:1) |
| `8080:80` | host `8080` → container `80` |
| `127.0.0.1:80:80` | bind to a specific host IP only |
| `53/udp` | UDP (TCP assumed otherwise) |

Bare ports and ranges bind on all interfaces (reachable from other machines); scope them to localhost with the `127.0.0.1:host:container` form. Published ports are preserved across `upgrade` and `reset`.

### d2k (Docker-compatible API)

When KubeSolo is installed with `--d2k`, set up a local Docker context that talks to the in-cluster translator over mTLS:

```bash
kubesoloctl d2k fetch                 # default instance
kubesoloctl d2k fetch --name prod     # a named instance

docker --context kubesolo ps          # use it
```

`d2k fetch` copies the CA + client certificate out of the container into `~/.docker/d2k/<name>/` and creates (or updates) a Docker context pointing at the container's published d2k port. The host port is random, so re-run `d2k fetch` after each upgrade or reset. See [d2k.md](../configuration/d2k.md) for the full integration.

---

## Multiple instances

Use `--name` to run independent KubeSolo instances side by side in container mode. Each gets its own container (`kubesolo-<name>`), data volume, kubeconfig context, and (with d2k) Docker context. Pass the same `--name` to `upgrade`, `reset`, `uninstall`, `kubeconfig fetch`, and `d2k fetch` to target a specific instance.

---

## Offline / air-gapped

`download` assembles a self-contained bundle — the KubeSolo release tarball, the `kubesoloctl` binary for the target architecture, and a `SHA256SUMS` — for transfer to an air-gapped machine. Both downloads are verified against the release's published checksums:

```bash
# On a connected machine (specify --arch when the target differs):
kubesoloctl download --version=v1.2.1 --path=./bundle --arch=arm64

# On the target machine:
sudo ./kubesoloctl install --offline-install=./bundle/kubesolo-v1.2.1-linux-arm64.tar.gz
```
