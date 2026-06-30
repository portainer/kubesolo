# KubeSolo e2e smoke harness

A small Bash harness that boots KubeSolo **as a container** (the artifact we
ship), waits for the cluster to become ready, and runs smoke assertions. It is
driven by `kubesoloctl` so it exercises the same install path users do.

## Scripts

| Script | Role |
|--------|------|
| `lib.sh` | Shared helpers + tunables (sourced, never run directly) |
| `boot.sh` | `kubesoloctl install --run-mode=container`, then gate on node / CoreDNS / local-path readiness |
| `smoke.sh` | Schedule a pod, verify in-cluster DNS and pod egress |
| `manifests.sh` | Tiered manifest deployment suite (see below) |
| `teardown.sh` | `kubesoloctl uninstall --purge` (+ `docker rm -f` fallback); best-effort |
| `run.sh` | Per-PR entrypoint: boot → smoke → manifests, always tears down via an `EXIT` trap |
| `soak.sh` | Nightly stability entrypoint: loop the tiers, watch container restarts + idle-RSS growth (leak detector) |
| `conformance.sh` | Nightly entrypoint: focused single-node-safe upstream `[Conformance]` subset via Sonobuoy |

## Manifest deployment tiers (`manifests.sh` + `manifests/`)

Each tier applies its manifests, gates on readiness with `kubectl wait`/`rollout
status`, asserts behaviour, then deletes its namespace(s). Set
`E2E_SKIP_MANIFESTS=1` to run smoke-only.

| Tier | Dir | Asserts |
|------|-----|---------|
| 1 — workload & networking | `manifests/01-workload` | Deployment rollout, ClusterIP routing (in-cluster), NodePort allocation |
| 2 — storage | `manifests/02-storage` | local-path PVC; data written by one pod survives into another |
| 3 — config & identity | `manifests/03-config` | ConfigMap env, mounted Secret, projected ServiceAccount token |
| 4 — controllers | `manifests/04-controllers` | Job completes, CronJob schedulable, DaemonSet + StatefulSet roll out |
| 5 — DNS & LoadBalancer | `manifests/05-dns-lb` | Cross-namespace Service DNS; LoadBalancer webhook assigns the node IP |

## Running locally

Requires Docker, `kubectl`, and a `kubesoloctl` binary. Privileged containers
must be allowed (KubeSolo needs `--privileged` for cgroup delegation, mount
propagation and iptables).

```bash
# Build kubesoloctl and run against a locally built image:
make build-kubesoloctl
IMAGE=portainer/kubesolo:dev KUBESOLOCTL=./dist/kubesoloctl test/e2e/run.sh

# …or via the Makefile target:
make test-e2e E2E_IMAGE=portainer/kubesolo:dev
```

> Note: KubeSolo is Linux-only. On macOS it can only run inside a Linux VM/container,
> so the harness is intended for Linux hosts and Linux CI runners.

## Tunables (environment)

| Var | Default | Meaning |
|-----|---------|---------|
| `IMAGE` | `portainerci/kubesolo:develop` | Image under test (override per PR, e.g. `portainerci/kubesolo:pr-12`) |
| `KUBESOLOCTL` | `kubesoloctl` | Path to the kubesoloctl binary |
| `KUBECTL` | `kubectl` | Path to kubectl |
| `KS_NAME` | `kubesolo` | Instance name / kube context |
| `READY_TIMEOUT` | `240` | Seconds to wait for readiness gates |

## Why this leans on kubesoloctl (the seam)

`kubesoloctl install --run-mode=container` already does everything the harness
would otherwise hand-roll, which is why there is no raw `docker run` here:

- **Pulls the image** itself (`ImagePull`), so no separate `docker pull` step.
- Runs a **privileged** container with `cgroupns=host`, a persistent data volume,
  and publishes `6443/tcp` on a **random** `127.0.0.1` host port.
- **Waits for the API server** and **merges a host-reachable kubeconfig**
  (server rewritten to `https://127.0.0.1:<random-port>`, context `KS_NAME`) into
  `~/.kube/config`.

Because the published port is random, do **not** assume `6443:6443` — always go
through the merged kubeconfig (`kubectl --context "$KS_NAME"`). `kubesoloctl
kubeconfig view` returns the *in-container* kubeconfig (wrong host port) and is
not suitable for host access.

## Scope

Smoke suite + manifest deployment tiers. Performance/footprint measurement and a
scheduled conformance/density run are layered on separately (Phase 6).
