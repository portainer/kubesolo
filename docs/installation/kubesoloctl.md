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

Requires a running container engine with the Docker API (Docker Engine, Docker Desktop, OrbStack, Colima, Rancher Desktop with moby, …). Like the `docker` CLI, kubesoloctl uses `DOCKER_HOST` when it is set and otherwise the active Docker context (`docker context use …`), so it reaches engines that do not provide `/var/run/docker.sock`.

```bash
# On macOS this is implied; elsewhere request it explicitly:
kubesoloctl install --run-mode=container
```

KubeSolo starts in a container, the API server is published on a free localhost port, which is kept across restarts, upgrades and resets, and your kubeconfig is merged and pointed at it automatically. Container mode also adjusts cgroups, mounts, DNS, and eviction thresholds so the node comes up cleanly inside a container.

```bash
kubectl get nodes --watch
```

If your kubeconfig entry is ever lost or out of date, fetch it again:

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
| `load` | Load images from the container engine into a container instance |
| `list` | List the container instances on the container engine |
| `logs` | Print what KubeSolo logged in a container instance (`-f` to follow) |
| `d2k fetch` | Set up a Docker context for the d2k endpoint (container mode) |
| `download` | Build an offline install bundle for an air-gapped machine |
| `version` | Print version information |

### install

Common flags:

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--version` | `KUBESOLO_VERSION` | matched to the CLI build | KubeSolo version to install |
| `--path` | `KUBESOLO_PATH` | `/var/lib/kubesolo` | Data directory (ignored in container mode, which keeps state in a volume) |
| `--run-mode` | `KUBESOLO_RUN_MODE` | `service` | `service`, `daemon`, `foreground`, or `container` |
| `--name` | `KUBESOLO_NAME` | `kubesolo` | Instance name (container name + kubeconfig context) |
| `--image` | `KUBESOLO_IMAGE` | `portainer/kubesolo:<version>` | Container image (container mode). It is pulled first; an image the engine already has but cannot pull, such as one built locally or loaded with `docker load` for an offline install, is used as it is |
| `--container-ports` | `KUBESOLO_CONTAINER_PORTS` | _(none)_ | Workload host ports to publish (container mode) |
| `--container-mounts` | `KUBESOLO_CONTAINER_MOUNTS` | _(none)_ | Host directories to mount into the container (container mode) |
| `--d2k` | `KUBESOLO_D2K` | `false` | Enable the Docker-compatible API translator |
| `--local-storage` | `KUBESOLO_LOCAL_STORAGE` | on (see below) | Enable the local-path storage provisioner |
| `--node-ip` | `KUBESOLO_NODE_IP` | _(auto-detect)_ | Override the detected node IP |
| `--mtu` | `KUBESOLO_MTU` | _(auto-detect)_ | Override the detected MTU (ignored in container mode) |
| `--portainer-edge-image` | `KUBESOLO_PORTAINER_EDGE_IMAGE` | `docker.io/portainer/agent:lts` | Image deployed for the Portainer edge agent |
| `--offline-install` | `KUBESOLO_OFFLINE_INSTALL` | _(none)_ | Install from a local tarball/binary instead of downloading (host mode only) |
| `--proxy` | `KUBESOLO_PROXY` | _(none)_ | HTTP/HTTPS proxy injected into the service environment, or the container's in container mode (kept across `upgrade` and `reset`) |
| `--cpu-manager-policy` | `KUBESOLO_CPU_MANAGER_POLICY` | `none` | `static` gives Guaranteed-QoS pods exclusive cores (ignored in container mode) |
| `--cpu-manager-policy-options` | `KUBESOLO_CPU_MANAGER_POLICY_OPTIONS` | _(none)_ | Comma-separated `key=value` options for the static policy |
| `--reserved-cpus` | `KUBESOLO_RESERVED_CPUS` | `0` when the static policy is used | Cpuset reserved for the host and KubeSolo itself |
| `--system-reserved` | `KUBESOLO_SYSTEM_RESERVED` | _(none)_ | Resources withheld from allocatable, e.g. `cpu=1,memory=500Mi` |

`--local-storage` can only turn the provisioner on, and it is already on by default in KubeSolo, so the local-path provisioner is installed either way. `--local-storage=false` does not disable it. On a host install, disable it afterwards with `sudo kubesoloctl config set storage.localPath.enabled false` and restart KubeSolo. That only stops it being deployed on later starts; the existing provisioner keeps running until you remove it with `kubectl delete namespace local-path-storage` and `kubectl delete storageclass local-path`.

On a host install of KubeSolo v1.2.1 or later, `install` writes the settings to `/etc/kubesolo/config.yaml` and the service runs with `--config` only. Unlike the shell installer, it does not fold `KUBESOLO_*` variables for settings it has no flag for into that file. See [Configuration file](../configuration/config-file.md).

In container mode, `install` writes the configuration file into a `kubesolo-<name>-config` volume mounted at `/etc/kubesolo`, and the container runs with `--config` only. Settings that mean nothing inside a container are left out of it, and `install` reports them as ignored: `--path`, `--node-ip`, `--mtu`, `--cpu-manager-policy`, `--cpu-manager-policy-options` and `--reserved-cpus` — kubesoloctl or the container decides these (the data volume, the container's address and network, its cpuset) — as well as the LoadBalancer IP, node name, runtime endpoint and API settings of the file. The configuration volume survives `reset`; `uninstall` removes it unless `--keep-config` is given.

In container mode, settings are validated the same way before the image is pulled or a running container is replaced, so a bad value fails the install instead of leaving a container that restarts forever.

CPU pinning flags are validated before anything is installed, so a bad cpuset or an unsupported policy option fails immediately rather than crash-looping the service. See the [CPU pinning guide](../configuration/cpu-pinning.md).

### kubeconfig

```bash
kubesoloctl kubeconfig            # print the admin kubeconfig (host mode)
kubesoloctl kubeconfig view       # print it (reads from the container in container mode)
kubesoloctl kubeconfig fetch      # merge it into ~/.kube/config
```

`fetch` reads from the container when one is running, otherwise from the local data directory.

`install` and `fetch` merge into the first file listed in `$KUBECONFIG` when it is set, and into `~/.kube/config` otherwise, backing up an existing file first. They edit the file directly, so `kubectl` does not need to be installed.

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

As on a host, a container upgrade refuses a version that is not newer than the
running one unless `--force` is given. An instance running a release image
(`portainer/kubesolo`) moves to the release image of `--version`. One running
any other image, such as a CI build or a private mirror, needs `--image` to name
the image to upgrade to, rather than being switched to the public release:

```bash
kubesoloctl upgrade --name dev --version=v1.2.3 --image=registry.example.com/kubesolo:v1.2.3
```

`--offline-install`, `--sha256` and `--detach` apply to host installs only.

A container upgrade is backed up and rolled back like a host upgrade. The new
image is pulled while the cluster keeps running; a pull that fails changes
nothing. The container is then stopped, and its datastore copied inside the data
volume, together with the image and command line that ran on it. If the new
version's node is not Ready within `--health-timeout`, the datastore is restored
and the previous container is put back. `kubesoloctl rollback --name <name>`
undoes the last upgrade later, as on a host, losing whatever was changed in the
cluster since. A reset removes the backup along with the rest of the cluster
state. `kubesoloctl status --name <name>` shows the instance's version,
image, container state, API server, node health and rollback target.

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

### Mounting host directories

`--container-mounts` mounts host directories into the KubeSolo container — the same model as Kind's `extraMounts` — so pods can use them as `hostPath` volumes:

```bash
kubesoloctl install --container-mounts=/Users/me/data:/data,/Users/me/models:/models:ro
```

Each entry is `SRC:DST`, or `SRC:DST:ro` for read-only, with absolute paths; a pod then mounts `hostPath: {path: /data}`. On macOS the source must be a directory your engine shares with its VM (OrbStack and Docker Desktop share `/Users`; Colima shares your home directory). A mount can also place [containerd registry configuration](../configuration/registry.md) inside the data volume, for example `--container-mounts=/Users/me/registry:/var/lib/kubesolo/containerd/registry:ro`. Mounts are preserved across `upgrade` and `reset`; reinstall with `--container-mounts` to change them.

### Resource limits

KubeSolo and its pods run inside the container's own cgroup, so `docker stats` shows what the whole node uses, and a limit set on the container (for example `docker update --memory=2g --memory-swap=2g kubesolo`) bounds KubeSolo and every pod together, as it would a kind node. Leave room for KubeSolo itself, about 250 MB: a limit below what KubeSolo needs makes it thrash rather than fail.

### Loading local images

`load` copies images from the container engine into a container instance, so pods can run an image you built without pushing it to a registry — what `kind load docker-image` does:

```bash
docker build -t my-app:dev .
kubesoloctl load my-app:dev              # --name for a named instance
kubectl create deployment my-app --image=my-app:dev
```

Pods refer to the image by the name it has in the engine. An image tagged `:latest` (or untagged) defaults to `imagePullPolicy: Always` and is pulled again; use another tag, or set `imagePullPolicy: IfNotPresent`. `load` needs a KubeSolo release newer than v1.2.2 in the container.

### d2k (Docker-compatible API)

When KubeSolo is installed with `--d2k`, set up a local Docker context that talks to the in-cluster translator over mTLS:

```bash
kubesoloctl d2k fetch                 # default instance
kubesoloctl d2k fetch --name prod     # a named instance (context: kubesolo-prod)

docker --context kubesolo ps          # use it
```

`d2k fetch` copies the CA + client certificate out of the container into `~/.docker/d2k/<container>/` and creates (or updates) a Docker context, named after the container, pointing at the container's published d2k port. The host port is kept across restarts, upgrades and resets; a reset issues new certificates, so re-run `d2k fetch` after one. See [d2k.md](../configuration/d2k.md) for the full integration.

---

## Multiple instances

Use `--name` to run independent KubeSolo instances side by side in container mode. Each gets its own container (`kubesolo-<name>`), data volume, kubeconfig cluster, user and context, and (with d2k) Docker context, all named after the container: the instance `dev` is the kubeconfig context `kubernetes-admin@kubesolo-dev` and the Docker context `kubesolo-dev`. The default instance keeps the plain name `kubesolo`. Pass the same `--name` to `upgrade`, `reset`, `uninstall`, `kubeconfig fetch`, `kubeconfig view`, and `d2k fetch` to target a specific instance.

KubeSolo's admin users are scoped the same way (`kubernetes-admin@<cluster>`), so installing an instance never replaces the credentials of another instance, or of a kubeadm cluster whose admin user is also called `kubernetes-admin`. Entries merged by an older kubesoloctl, under the bare instance name or the shared `kubernetes-admin` user, are left alone by `uninstall`; delete them with `kubectl config` if you no longer need them.

---

## Offline / air-gapped

`download` assembles a self-contained bundle — the KubeSolo release tarball, the `kubesoloctl` binary for the target architecture, and a `SHA256SUMS` — for transfer to an air-gapped machine. Both downloads are verified against the release's published checksums:

```bash
# On a connected machine (specify --arch when the target differs):
kubesoloctl download --version=v1.2.2 --path=./bundle --arch=arm64

# On the target machine:
sudo ./kubesoloctl install --offline-install=./bundle/kubesolo-v1.2.2-linux-arm64.tar.gz
```
