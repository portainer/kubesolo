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
| `run.sh` | Per-PR entrypoint: boot → smoke → manifests → authentication, always tears down via an `EXIT` trap |
| `soak.sh` | Nightly stability entrypoint: loop the tiers, watch container restarts + idle-RSS growth (leak detector) |
| `conformance.sh` | Nightly entrypoint: focused single-node-safe upstream `[Conformance]` subset via Sonobuoy |
| `restart.sh` | `POST /api/v1/restart` on a **host** install (not container mode), through the agent's socket from a pod: a change is applied with every pod kept; a bad agent image and a configuration KubeSolo cannot run on are reverted; set-once agent settings are refused; agent settings from the environment and an agent already crash-looping do not fail it. Runs as root, as its own systemd unit, and puts the configuration back when it ends |
| `authentication.sh` | Called by `run.sh` after smoke and manifests. JWTs are rejected until an authentication file is set; once it is, tokens map to users and tokens for another audience are rejected. A replaced file loads without a restart, an invalid one is refused while the last valid one stays in use, and correcting it recovers (upstream's reload cases) |

## Manifest deployment tiers (`manifests.sh` + `manifests/`)

Each tier applies its manifests, gates on readiness with `kubectl wait`/`rollout
status`, asserts behaviour, then deletes its namespace(s). Set
`E2E_SKIP_MANIFESTS=1` to skip the manifest tiers. Authentication still runs.

| Tier | Dir | Asserts |
|------|-----|---------|
| 1 — workload & networking | `manifests/01-workload` | Deployment rollout, ClusterIP routing (in-cluster), NodePort allocation |
| 2 — storage | `manifests/02-storage` | local-path PVC; data written by one pod survives into another |
| 3 — config & identity | `manifests/03-config` | ConfigMap env, mounted Secret, projected ServiceAccount token |
| 4 — controllers | `manifests/04-controllers` | Job completes, CronJob schedulable, DaemonSet + StatefulSet roll out |
| 5 — DNS & LoadBalancer | `manifests/05-dns-lb` | Cross-namespace Service DNS; LoadBalancer webhook assigns the node IP |

## Running locally

Requires Docker, `kubectl`, `curl`, Go, and a `kubesoloctl` binary. Privileged containers
must be allowed (KubeSolo needs `--privileged` for cgroup delegation, mount
propagation and iptables). Go builds the authentication issuer in
`test/e2e/oidc`; that binary runs on the host, so the build uses `GOHOSTOS`
and `GOHOSTARCH`.

```bash
# Build kubesoloctl and run against a locally built image:
make build-kubesoloctl
IMAGE=portainer/kubesolo:dev KUBESOLOCTL=./dist/kubesoloctl test/e2e/run.sh

# A local engine image can be passed by id (no registry fetch):
# IMAGE=sha256:<docker image id> KUBESOLOCTL=./dist/kubesoloctl test/e2e/run.sh

# …or via the Makefile target:
make test-e2e E2E_IMAGE=portainer/kubesolo:dev
```

> Note: KubeSolo is Linux-only. On macOS it can only run inside a Linux VM/container,
> so the harness is intended for Linux hosts and Linux CI runners.

`run.sh` creates a temporary directory, bind-mounts it read-only at
`/etc/kubesolo/e2e-auth`, boots the cluster without a JWT authentication file, runs
smoke and the manifest tiers, then `authentication.sh`. That script serves a
local issuer on the container network gateway, writes an authentication file
mapping usernames to `k8s-<sub>`, points `kubernetes.apiServer.authenticationConfigFile`
at `/etc/kubesolo/e2e-auth/authentication.yaml`, and restarts the same container
once. Before that restart an issuer token must get 401; after it, the token
must map to `k8s-<sub>` and a token for another audience must get 401. It then
replaces the file with a `panda-<sub>` mapping, then with one whose username
expression does not compile, and finally with the `k8s-<sub>` mapping again.
The invalid file must raise kube-apiserver's
`apiserver_authentication_config_controller_automatic_reloads_total{status="failure"}`
while `panda-<sub>` keeps working, and none of these replacements may restart
the container. The `EXIT` trap uninstalls the instance and
deletes the temporary directory even when the fixture build fails.

`RELOAD_TIMEOUT` is how long authentication waits for each username mapping (180
seconds by default).

## Tunables (environment)

| Var | Default | Meaning |
|-----|---------|---------|
| `IMAGE` | `portainerci/kubesolo:develop` | Image under test (override per PR, e.g. `portainerci/kubesolo:pr-12`) |
| `KUBESOLOCTL` | `kubesoloctl` | Path to the kubesoloctl binary |
| `KUBECTL` | `kubectl` | Path to kubectl |
| `KS_NAME` | `kubesolo` | Instance name / kube context |
| `READY_TIMEOUT` | `240` | Seconds to wait for readiness gates |
| `RELOAD_TIMEOUT` | `180` | Seconds to wait for an authentication file reload |

## Why this leans on kubesoloctl (the seam)

`kubesoloctl install --run-mode=container` already does everything the harness
would otherwise hand-roll, which is why there is no raw `docker run` here:

- **Pulls the image** itself (`ImagePull`), so no separate `docker pull` step.
- Runs a **privileged** container with `cgroupns=host`, a persistent data volume,
  and publishes `6443/tcp` on a **random** `127.0.0.1` host port.
- **Waits for the API server** and **merges a host-reachable kubeconfig**
  (server rewritten to `https://127.0.0.1:<port>`, context
  `kubernetes-admin@<container name>`) into
  `~/.kube/config`.

Because the published port is chosen at install, do **not** assume `6443:6443` —
always go through the merged kubeconfig (the `kc` helper resolves the context). `kubesoloctl
kubeconfig view` returns the *in-container* kubeconfig (wrong host port) and is
not suitable for host access.

## Scope

Smoke suite, manifest deployment tiers, and the container authentication checks
in `run.sh`. Performance/footprint measurement and a scheduled conformance/density
run are layered on separately (Phase 6).
