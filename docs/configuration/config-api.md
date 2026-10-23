# Configuration API

KubeSolo can serve its configuration over a unix socket, so the settings in
[`/etc/kubesolo/config.yaml`](config-file.md) can be read and changed
programmatically rather than by editing the file.

It is off by default.

```yaml
api:
  enabled: true
  socketPath: ""   # empty = <path>/config.sock
```

Restart KubeSolo after enabling it.

---

## What it does and does not do

**It manages desired state.** KubeSolo reads its configuration once during
startup and derives every path and component argument from it, so a change made
here takes effect when KubeSolo restarts. Every mutating response says which
settings changed and that a restart is needed — it does not imply otherwise.

**There is no live reload.** A future release may add one for the small set of
settings that can safely change at runtime.

---

## Access

A unix socket, mode `0600`, owned by root. The file permissions are the whole
authorisation model: there is no token and no TLS, because there is no network
listener to protect. On an edge device, a mutating endpoint that cannot be
reached over the network is worth more than one that can be reached and
authenticated.

Practically, that means anything able to open the socket can change KubeSolo's
configuration. Reaching it from another machine means going through something
that already has an authenticated channel — the Portainer Edge Agent, or SSH.

> The Edge Agent cannot reach the socket today: its deployment declares no
> hostPath volumes. Remote configuration through Portainer needs that mount
> added first.

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/config` | Read the configuration |
| `GET` | `/api/v1/config/schema` | Every setting, its type, default and mutability |
| `PUT` | `/api/v1/config` | Replace the configuration |
| `PATCH` | `/api/v1/config` | Change part of the configuration |
| `POST` | `/api/v1/config:validate` | Check a candidate without saving |
| `DELETE` | `/api/v1/config` | Reset every setting to its default |
| `GET` | `/healthz` | Liveness |

### Reading

```bash
curl -s --unix-socket /var/lib/kubesolo/config.sock \
  http://localhost/api/v1/config
```

Returns the **stored** document — what is in the file — not the settings the
running process resolved. They differ when a flag or environment variable
overrides the file. Returning the resolved values would mean a read-modify-write
cycle silently writing those overrides into the file permanently.

`portainer.edgeKey` is returned as `***`. Add `?showSecrets=true` for the real
value.

The response carries an `ETag`. Pass it back as `If-Match` on a write to be told
if the configuration changed underneath you.

### Changing part of it

An [RFC 7386](https://www.rfc-editor.org/rfc/rfc7386) JSON merge patch. This is
the usual way to change a setting.

```bash
curl -s -X PATCH --unix-socket /var/lib/kubesolo/config.sock \
  -H 'Content-Type: application/merge-patch+json' \
  -d '{"network":{"mtu":1400}}' \
  http://localhost/api/v1/config
```

Settings the patch does not mention are left alone. A `null` **removes** a
setting, restoring its default — the same thing deleting the line from the file
would do:

```bash
# back to the default namespace
-d '{"d2k":{"namespace":null}}'
```

### Replacing all of it

```bash
curl -s -X PUT --unix-socket /var/lib/kubesolo/config.sock \
  -H 'Content-Type: application/json' \
  --data-binary @config.json \
  http://localhost/api/v1/config
```

`PUT` replaces: a setting the body omits is **removed**, returning to its
default. Use `PATCH` to change part of the configuration.

A `PUT` that sends `portainer.edgeKey` back as `***` is refused. Redaction would
otherwise make the obvious read-modify-write cycle overwrite the real credential
with the placeholder. Send the real value, or omit the setting to keep the stored
one.

### Checking without saving

```bash
curl -s -X POST --unix-socket /var/lib/kubesolo/config.sock \
  -H 'Content-Type: application/json' \
  -d '{"d2k":{"enabled":true}}' \
  http://localhost/api/v1/config:validate
```

Reports what would change and any warnings. Writes nothing.

### Resetting

```bash
curl -s -X DELETE --unix-socket /var/lib/kubesolo/config.sock \
  http://localhost/api/v1/config
```

`path` is immutable and is carried over.

---

## Responses

Every successful mutation returns:

```json
{
  "config": { "...": "the saved document, secrets redacted" },
  "changed": ["network.mtu"],
  "requiresRestart": ["network.mtu"],
  "restartRequired": true,
  "warnings": []
}
```

Failures return:

```json
{ "error": "...", "field": "network.mtu" }
```

| Status | Meaning |
|---|---|
| `200` | Applied, or validated |
| `400` | Malformed body, or a secret sent back as `***` |
| `409` | An immutable setting would change — only `path` |
| `412` | `If-Match` did not match; the configuration changed since you read it |
| `415` | Wrong `Content-Type` on a patch |
| `422` | Valid JSON, invalid configuration |
| `500` | The file could not be read or written |

**A rejected request writes nothing.** Validation runs before the file is
touched, so `409`, `412` and `422` all leave the existing configuration exactly
as it was.

---

## What is logged

Every request is recorded, which is the audit trail for an endpoint whose job is
changing how KubeSolo starts.

```
info  configapi PATCH /api/v1/config status=200 changed=["network.mtu"] restart_required=true
warn  configapi PATCH /api/v1/config status=409 reason="path cannot be changed on an existing installation..."
debug configapi GET   /api/v1/config status=200
```

Writes are logged at **info**, rejections at **warn** with the reason the client
was given, and reads at **debug** so a user interface polling the configuration
does not bury the changes among its own reads. Health checks are not logged.

**Only setting paths are recorded, never values.** A line says
`changed=["portainer.edgeKey"]` — that the credential was replaced, not what it
was replaced with.

```bash
journalctl -u kubesolo | grep configapi
```

This is also how to confirm that `kubesoloctl config set` went through the API
rather than editing the file: a write through the API leaves a line, a direct
file edit does not.

---

## Concurrency

Writes are serialised, so two simultaneous patches cannot each read the document
and have one change lost.

A writer outside the process — someone running `kubesoloctl config set`, or
editing the file — cannot be serialised against. Use `If-Match` to detect it:

```bash
ETAG=$(curl -si --unix-socket /var/lib/kubesolo/config.sock \
  http://localhost/api/v1/config | grep -i '^etag:' | cut -d' ' -f2 | tr -d '\r')

curl -s -X PATCH --unix-socket /var/lib/kubesolo/config.sock \
  -H 'Content-Type: application/merge-patch+json' \
  -H "If-Match: $ETAG" \
  -d '{"network":{"mtu":1400}}' \
  http://localhost/api/v1/config
```

A `412` means the configuration moved; read it again and reapply.

---

## Applying changes

A saved change takes effect when KubeSolo restarts. `POST /api/v1/restart` does
that restart for you, so a client running inside KubeSolo, such as the Portainer
agent, can apply a change it cannot restart the host service for itself. It is
served when the API is enabled, alongside the [upgrade API](upgrade-api.md).

```bash
curl -s -X POST --unix-socket /var/lib/kubesolo/config.sock \
  http://localhost/api/v1/restart
```

Body (optional): `{"healthTimeoutSeconds": 600}`. This is how long KubeSolo has
to finish starting and come back healthy. Default 600, minimum 60.

The restart runs out of process, the same way an upgrade does: the API checks
the request, hands it to the executor and answers before anything is stopped.
The executor stops KubeSolo, starts it again and waits until:

- KubeSolo has finished starting and is healthy: the API server is ready, the
  node is Ready, CoreDNS has a ready pod, and KubeSolo has stayed up as one
  process for 30 seconds. Waiting for the start to finish means the checks see
  the restarted KubeSolo, not the node and pod statuses the previous one left.
- The Portainer agent, if one was running, is still the same pod and the same
  container it was before. If KubeSolo changed the agent's pod template as it
  started, for a new `portainer.image` or a change to the API settings, the new
  pod must be ready instead. An agent that was not running steadily before the
  restart is not checked: one in a crash loop, or whose container exited in
  the five minutes before, is ready only between crashes, and the restart
  cannot be told apart from the crashes.

Workloads, the agent among them, keep running through the restart: KubeSolo
stops without stopping its containers and reattaches to them when it starts. On
systemd the executor makes sure of this, setting `KillMode=process` on units
written before the installer did. This is verified on systemd only; OpenRC, s6,
runit, SysV and Upstart are not yet tested.

### When it fails

KubeSolo records the configuration file as **known-good** once it, and the
Portainer agent if one is configured, have run healthy on it for a minute, in
`<path>/upgrade/config.good.yaml`. If a restart leaves KubeSolo unhealthy, or
the agent not running, the executor puts the known-good configuration back and
restarts KubeSolo on it. The configuration that failed is kept as
`<path>/upgrade/config.failed.yaml`, replacing the one an earlier revert kept.
Both files hold the edge key, so they are readable by root only, and they are
removed with the rest of KubeSolo's data on uninstall.

While the agent is not running, KubeSolo does not record the configuration as
known-good, and its log says why once, with a `warn` from the `upgrade`
component:

```
not recording this configuration as known-good, so a restart that fails goes back to an older one, or cannot be reverted: the Portainer agent is not running: no ready pod runs docker.io/portainer/agent:2.31.0
```

It logs again when the agent is running and recording resumes.

That is what keeps an edge device reachable: a change that takes down KubeSolo
or the agent would otherwise cut off the only way to correct it from Portainer.
The containers keep running throughout, so once KubeSolo is back on the
known-good configuration the agent picks up where it left off.

Nothing is reverted when there is no known-good configuration yet, when the
configuration being applied is the known-good one, or when KubeSolo could not
be stopped.

### What a restart does not apply

`portainer.edgeID`, `portainer.edgeKey` and `portainer.async` are
[set once](config-file.md#the-portainer-agents-settings-are-set-once): KubeSolo
gives them to the agent when it first deploys it, and never again. Once KubeSolo
has started with an agent configured, a `PATCH` or `PUT` that changes any of
them, or clears them, is refused with `409` and the setting named in `field`.
Before that, a mistyped value can still be corrected. A `DELETE` keeps them, as
it keeps `path`. If the file is edited by hand to change one, a restart is
refused with `409` rather than reporting success while the agent keeps the old
value. Changing them on an existing agent is not supported.

```json
{"id": "7c1e9d04a2b3", "operation": "restart", "from": "v1.2.2", "to": "v1.2.2", "statusPath": "/api/v1/status"}
```

| Status | When |
|---|---|
| `202` | Accepted. Follow it with `GET /api/v1/status`. |
| `400` | Malformed body, unknown field, or a `healthTimeoutSeconds` out of range. |
| `409` | An upgrade, rollback or restart is already in progress, from the API or from `kubesoloctl`. Or the file was edited by hand to change `path`, which cannot change on an existing installation, or one of the Portainer agent's set-once settings; see below. `field` names the setting. |
| `422` | KubeSolo would refuse to start with the stored configuration, for example after a hand edit. Nothing was stopped. |
| `501` | KubeSolo runs in a container, which cannot restart itself. Restart the container from the host. |

### Following it

The socket goes away while KubeSolo is stopped and comes back once it has
started, so expect connection errors for a few seconds. Then poll
`GET /api/v1/status`. The restart is recorded with the upgrade runs, under
`upgrade`, with `"operation": "restart"`:

- While it runs, `upgrade.inFlight` is `true` and `upgrade.current.phase` moves
  through `queued`, `preflight`, `stopping`, `starting` and `verifying`. If it
  is reverted, it stays `rolling-back` while the known-good configuration is
  applied; `upgrade.current.messages` lists the steps. Changes to the
  configuration are refused with `409` until it ends: a revert would replace
  them; so is a change through `kubesoloctl config`, which edits the file
  directly when the API is off or KubeSolo is stopped.
- If the executor dies part-way, the restarted KubeSolo closes the run once it
  has been healthy for a minute: `succeeded` if the Portainer agent is
  running, `failed` if it is not.
- Once it ends, `upgrade.last` has the same `id` as the `202`, `phase: "done"`
  and a `result`:

| Result | Meaning |
|---|---|
| `succeeded` | KubeSolo restarted, passed the health checks, and the agent is running. The configuration is in effect. |
| `rolled-back` | The configuration failed and the known-good one was put back. KubeSolo and the agent are running on it. `upgrade.last.error` says why the change failed. |
| `aborted` | Failed before KubeSolo was stopped, for example when the service could not be found. Nothing changed. |
| `failed` | KubeSolo did not stop, or the configuration failed and could not be reverted, or the known-good configuration failed too. `upgrade.last.error` says which. Needs attention. |

---

## See also

- [Upgrade API](upgrade-api.md) — upgrade, rollback and status over the same socket
- [Configuration file](config-file.md)
- [kubesoloctl](../installation/kubesoloctl.md) — `kubesoloctl config` uses this API when it is running
