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

## See also

- [Configuration file](config-file.md)
- [kubesoloctl](../installation/kubesoloctl.md) — `kubesoloctl config` uses this API when it is running
