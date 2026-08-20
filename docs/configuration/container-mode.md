# Container Mode

KubeSolo can run as a container instead of a host process. This is primarily a **development and CI** convenience — it lets you spin up a full single-node cluster on any machine with a container engine, without installing KubeSolo onto the host or wiring up an init system. The production path remains a native binary managed as a system service; container mode trades the edge memory profile for portability.

Container mode is enabled by the `--container-mode` flag and is **auto-detected** when KubeSolo finds itself running inside a container, so the bundled image works out of the box.

> If you just want to run KubeSolo in a container for local development, the simplest path is `kubesoloctl install --run-mode=container`, which builds the kubeconfig and publishes ports for you. See the [kubesoloctl guide](../installation/kubesoloctl.md). This document describes the underlying `--container-mode` behaviour that `kubesoloctl` and the container image rely on.

---

## Enabling container mode

| Flag | Env var | Default |
|---|---|---|
| `--container-mode` | `KUBESOLO_CONTAINER_MODE` | _(auto-detected)_ |

Container mode is active when **either** the flag/env var is set **or** KubeSolo detects a container environment. Auto-detection looks for any of:

- `/.dockerenv` (Docker)
- `/run/.containerenv` (Podman)
- a non-empty `container` environment variable (systemd-nspawn and others)

The published `portainer/kubesolo` image sets `CMD ["--container-mode"]`, so the flag is always present there; the auto-detection covers cases where KubeSolo is run inside a container by other means.

---

## What changes in container mode

Running a Kubernetes node inside a container means the kubelet, containerd, and kube-proxy operate against a restricted, often read-only, view of the host's cgroups, mounts, and networking. Container mode applies a set of adjustments so the node can come up cleanly in that environment:

| Area | Default (host) | Container mode |
|---|---|---|
| cgroup driver | `systemd` / `cgroupfs` per host | `cgroupfs`, with controller delegation set up on the root cgroup |
| Mount propagation | inherited from host | `/` remounted `rshared` so kubelet can propagate volume mounts (e.g. projected service-account tokens) into pods |
| kubelet QoS cgroups | enabled | `cgroupsPerQOS: false`, `enforceNodeAllocatable: []` — avoids the cgroupv2 "no internal processes" conflict |
| Eviction / image GC | upstream thresholds | relaxed (`memory.available: 50Mi`, disk thresholds `0%`, `imageGCHighThresholdPercent: 100`) so a containerised node isn't evicted by the host's disk usage |
| Pod DNS (`resolvConf`) | host `/etc/resolv.conf` | `/dev/null`, to prevent the host's DNS config leaking into pods |
| CoreDNS upstream | `forward . /etc/resolv.conf` | `forward . 1.1.1.1 8.8.8.8` (since the node `resolv.conf` is empty) |
| CoreDNS resources | memory limit `64Mi` | memory limit removed (requests retained) to avoid OOM under a constrained container memory limit |
| kube-proxy conntrack | upstream defaults | both set to `0` — avoids writing to `/proc/sys/net/netfilter/nf_conntrack_max`, which is often read-only inside a container |
| [CPU pinning](cpu-pinning.md) | available | unsupported — KubeSolo refuses to start with `--cpu-manager-policy=static`, since exclusive cores are bounded by the container's own cpuset, which KubeSolo does not control |

> **Container mode applies the adjustments above on top of the standard upstream Kubernetes defaults.** NodeSetter is still used in place of the scheduler, as always.

---

## Running with Docker

The container needs elevated privileges to manage cgroups, mounts, and networking for the nested workloads.

```bash
docker run -d --privileged \
  --hostname kubesolo \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --tmpfs /tmp --tmpfs /run \
  -v /lib/modules:/lib/modules:ro \
  -v kubesolo-data:/var/lib/kubesolo \
  -p 6443:6443 \
  --name kubesolo \
  portainer/kubesolo:latest
```

| Option | Why it's needed |
|---|---|
| `--privileged` | mount propagation, cgroup delegation, and iptables management for nested containers |
| `--security-opt seccomp=unconfined` / `apparmor=unconfined` | allows the syscalls containerd/runc need to start pods |
| `--tmpfs /tmp --tmpfs /run` | writable runtime directories |
| `-v /lib/modules:/lib/modules:ro` | kernel modules for networking (iptables, conntrack) |
| `-v kubesolo-data:/var/lib/kubesolo` | persists cluster state (kine DB, PKI, images) across restarts |
| `-p 6443:6443` | publishes the API server to the host |

The image exposes `6443` (API server) and `10250` (kubelet) and handles `SIGTERM` for graceful shutdown.

### Getting the kubeconfig

The admin kubeconfig is generated inside the container with an in-cluster server address; rewrite it to point at the published host port:

```bash
docker exec kubesolo cat /var/lib/kubesolo/pki/admin/admin.kubeconfig > kubeconfig.json
sed -i 's|https://[^"]*:6443|https://127.0.0.1:6443|' kubeconfig.json
export KUBECONFIG=$(pwd)/kubeconfig.json

kubectl get nodes --watch    # wait until STATUS: Ready
```

### Stopping

```bash
docker stop kubesolo && docker rm kubesolo
```

Remove the `kubesolo-data` volume as well to wipe cluster state.

---

## Running with Apple Containers

On macOS, [Apple's `container` runtime](https://github.com/apple/container) can run the image as well. The flags differ slightly from Docker — grant all capabilities with `--cap-add ALL` instead of `--privileged`:

```bash
container run -d \
  --name kubesolo \
  --cap-add ALL \
  --tmpfs /tmp \
  --tmpfs /run \
  -v kubesolo-data:/var/lib/kubesolo \
  -p 16443:6443 \
  portainerci/kubesolo:$TAG
```

Set `$TAG` to the image tag you want (e.g. a release version, or `pr-<number>` from CI). The example publishes the API server on host port `16443`; adjust the kubeconfig host accordingly:

```bash
container exec kubesolo cat /var/lib/kubesolo/pki/admin/admin.kubeconfig > kubeconfig.json
sed -i '' 's|https://[^"]*:6443|https://127.0.0.1:16443|' kubeconfig.json
export KUBECONFIG=$(pwd)/kubeconfig.json

kubectl get nodes --watch
```

> `sed -i ''` is the BSD/macOS form (an explicit empty backup suffix); on Linux use `sed -i` as shown in the Docker section above.

---

## Building the image

The published images are built and pushed by CI (`docker-build` / `docker-manifest` jobs) for every PR and release across `amd64`, `arm64`, `arm`, and `riscv64`. To build locally:

```bash
make image                          # current arch, tag: <image>:<version>-<os>-<arch>
make image GOARCH=arm64             # cross-compile
make image-buildx                   # multi-arch build + push
```

`make image` downloads the arch-specific dependencies, builds a static (musl) binary via Alpine, and packages it into the `Dockerfile` image. Override the image name or tag with `IMAGE_NAME` and `IMAGE_TAG`:

```bash
make image IMAGE_NAME=myorg/kubesolo IMAGE_TAG=dev
```

---

## Publishing workload ports

Because the node runs in its own network namespace, NodePort / LoadBalancer services and `hostPort` pods are only reachable from the host if the port is published when the container is created (`-p host:container`), the same model as Kind's `extraPortMappings`. `kubesoloctl` exposes this via `--container-ports`; with raw `docker run`, add the `-p` mappings you need up front. See the [kubesoloctl guide](../installation/kubesoloctl.md#publishing-workload-ports) for details.
