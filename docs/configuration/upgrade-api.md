# Upgrade API

KubeSolo can upgrade itself in place, with a backup and automatic rollback, when
asked over its [configuration API](config-api.md) socket. `kubesoloctl upgrade`,
`kubesoloctl rollback` and `kubesoloctl status` use the same endpoints when the
API is enabled, and run the same upgrade themselves when it is not.

```yaml
api:
  enabled: true
```

| Endpoint | Purpose |
|---|---|
| `POST /api/v1/upgrade` | Start an upgrade. Returns `202` once accepted. |
| `POST /api/v1/rollback` | Restore the version before the last upgrade. Returns `202`. |
| `GET /api/v1/status` | Running version, health, Portainer agent image, and the upgrade record. |

---

## How an upgrade runs

The request is checked and handed to an **executor**: the kubesolo binary run
as a separate process, outside the KubeSolo service (a transient systemd unit,
or its own session and cgroup on other init systems). It has to be: stopping
KubeSolo stops everything in its service, including whatever served the request.

The order is the point. Everything that can fail does so while KubeSolo is still
running and untouched:

```
fetch → checksum → arch/libc → version → disk space → backup → dry-run
                                                                  ↓
                                        stop → replace → start → verify
```

1. **Fetch** the release archive, or use the one named by `source`.
2. **Checksum** it against the request's `sha256`, the release's `SHA256SUMS`, a
   `SHA256SUMS` next to `source`, or — for GitHub releases that predate
   `SHA256SUMS` — the digest GitHub publishes for the asset. Nothing is installed
   without a match.
3. **Check the binary** is an executable for this host's architecture and C
   library, and reports the requested version.
4. **Check disk space** for the backup.
5. **Back up** the datastore (an online `VACUUM INTO` snapshot — one consistent,
   self-contained file), the current binary, and `/etc/kubesolo/config.yaml`.
6. **Dry-run**: the *new* binary opens a copy of the snapshot through kine,
   which applies kine's migrations, and checks it.
7. Stop KubeSolo, install the new binary, start it.
8. **Verify** for up to `healthTimeoutSeconds` (default 600): the API server is
   ready, the node is Ready and reports the new version, CoreDNS has a ready
   pod, and KubeSolo has stayed up as one process for 30 seconds.

A failure in steps 1–6 is an **abort**: nothing was changed and KubeSolo kept
running throughout. A failure in step 7 or 8 is **rolled back** automatically:
the previous binary, datastore and configuration are restored and started.

Workloads keep running across a successful upgrade; KubeSolo reattaches to them.

### The boot guard

The executor watches the new version, but it is not the only safeguard. Before
switching over, the upgrade installs a small shell script that the init system
runs before every start of KubeSolo (a systemd drop-in, or a line in the
OpenRC, SysV, s6, runit or Upstart service definition). While a new version is
unproven, it counts starts; after **3** starts that did not reach a healthy
state, it restores the previous binary, datastore and configuration on its own.
This covers the host losing power, or rebooting, partway through an upgrade.

---

## POST /api/v1/upgrade

```bash
curl --unix-socket /var/lib/kubesolo/config.sock -X POST http://localhost/api/v1/upgrade \
  -H 'Content-Type: application/json' -d '{"version": "v1.2.2"}'
```

| Field | Required | Meaning |
|---|---|---|
| `version` | yes | Release to install, e.g. `v1.2.2`. |
| `source` | no | Absolute path to a release archive (`.tar.gz`) or binary already on the host — for air-gapped upgrades. |
| `sha256` | no | Expected checksum of the archive or binary. Otherwise taken from `SHA256SUMS`. |
| `force` | no | Allow a version that is not newer than the running one. |
| `healthTimeoutSeconds` | no | How long the new version has to become healthy. Default 600, minimum 60. If it is rolled back, the restored version gets at least the default. |

```json
{"id": "3f9c2a1b7e40", "operation": "upgrade", "from": "v1.2.1", "to": "v1.2.2", "statusPath": "/api/v1/status"}
```

| Status | When |
|---|---|
| `202` | Accepted. Follow it with `GET /api/v1/status`. |
| `400` | Malformed request, unknown field, or a `source` that is not a file. |
| `409` | Another upgrade or rollback is in progress — from the API or from `kubesoloctl` — or the version is not newer and `force` is not set. |
| `501` | KubeSolo runs in a container, which cannot replace its own image. Upgrade it with `kubesoloctl upgrade` on the host. |

### Air-gapped upgrades

Put the release archive on the host with its `SHA256SUMS` beside it —
`kubesoloctl download --version=v1.2.2 --arch=arm64` produces exactly that — and
name it as `source`:

```json
{"version": "v1.2.2", "source": "/var/tmp/bundle/kubesolo-v1.2.2-linux-arm64.tar.gz"}
```

### Mirrors

Set `KUBESOLO_RELEASE_BASE_URL` in KubeSolo's environment (or kubesoloctl's) to
download from a mirror laid out like the GitHub release, e.g.
`https://mirror.example.com/kubesolo` serving `v1.2.2/kubesolo-v1.2.2-linux-amd64.tar.gz`.
A mirror must publish `SHA256SUMS` with each release.

---

## POST /api/v1/rollback

Restores the binary, datastore and configuration file from before the last
upgrade, and restarts the workloads.

> **Everything written to the cluster since that upgrade is lost.** The
> datastore goes back to the moment the previous version was replaced.

A rollback is only possible to the version immediately before the running one,
and only until the next upgrade replaces the backup. Restoring the datastore
together with the binary is what makes it safe: an older Kubernetes API server
reading objects a newer one wrote can silently drop fields it does not know.

Body (optional): `{"healthTimeoutSeconds": 600}`.

`409` when there is no valid backup — none was taken, or it belongs to an
upgrade other than the one that installed the running version.

---

## GET /api/v1/status

Answers from disk as well as from the live process, so a KubeSolo that has just
been rolled back can say what happened to it.

```json
{
  "version": "v1.2.2",
  "containerMode": false,
  "health": {"healthy": true, "checks": {"apiserver": "ok", "node": "ok", "coredns": "ok"}},
  "agent": {"configuredImage": "docker.io/portainer/agent:2.31.0", "runningImage": "docker.io/portainer/agent:2.31.0", "inSync": true},
  "upgrade": {
    "inFlight": false,
    "last": {"id": "3f9c2a1b7e40", "operation": "upgrade", "from": "v1.2.1", "to": "v1.2.2", "phase": "done", "result": "succeeded", "hostChanged": true, "messages": ["..."]},
    "rollbackTarget": {"from": "v1.2.1", "to": "v1.2.2", "created": "2026-10-08T01:02:03Z", "datastoreBytes": 2060288}
  }
}
```

`upgrade.current` is the run in flight, with its `phase`. `upgrade.last.result`
is one of:

| Result | Meaning |
|---|---|
| `succeeded` | The new version is running and passed the health checks. |
| `aborted` | Failed before anything changed. The previous version kept running. |
| `rolled-back` | The new version failed and the previous one was restored and is healthy. |
| `failed` | The host changed and could not be brought back to a healthy state. Needs attention. |

`upgrade.pendingVerify` and `upgrade.bootAttempts` show a new version the boot
guard is still counting; `upgrade.guardEvents` record any restore it made.

### Portainer agent image

`agent` compares the image in `portainer.image` with the one the agent
Deployment runs. A change to `portainer.image` is applied to the Deployment when
KubeSolo restarts. An image Portainer itself set on the Deployment is left
alone: KubeSolo records the image it last applied in the
`kubesolo.io/configured-image` annotation, and only a change to the configured
value is applied.

---

## The Portainer agent's socket

With the API enabled and a Portainer edge agent configured, KubeSolo also serves
the API on `<path>/agent-api/kubesolo.sock`, and mounts that directory into the
agent's pod (`KUBESOLO_API_SOCKET` names the socket). The directory is mounted,
not the socket: KubeSolo recreates the socket on every start, and a bind mount
of the file would keep pointing at the old one.

This gives the agent the same control as the socket's owner: configuration,
upgrade and rollback. The agent already runs as cluster-admin. Leave the API
disabled to withhold it.

---

## Files

Everything lives under `<path>/upgrade/`:

| File | Purpose |
|---|---|
| `state.json` | The run in flight and the last one to finish. |
| `lock` | Held by whoever is upgrading; the API and kubesoloctl both take it. |
| `backup/` | The previous binary, datastore snapshot, configuration file and `manifest.json`. One is kept. |
| `guard.sh` | The boot guard. |
| `pending`, `attempts` | Present while a new version is being verified. |
| `guard.log` | Restores made by the boot guard. |

## See also

- [Configuration API](config-api.md)
- [kubesoloctl](../installation/kubesoloctl.md)
