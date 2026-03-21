# Registry Mirrors

## Overview

A registry mirror (also called a pull-through proxy or proxy cache) is an intermediate server that sits between your cluster and an upstream container registry. When containerd pulls an image, it first checks the mirror; if the image is cached there, the mirror serves it directly without reaching the upstream.

Common reasons to use a registry mirror:

- **Rate limiting** — Docker Hub enforces pull rate limits for unauthenticated and free-tier users. A mirror amortises those limits across your fleet.
- **Air-gapped environments** — Devices that cannot reach the internet can pull images from an internal mirror that replicates the registries you care about.
- **Latency / bandwidth** — A mirror co-located in your datacenter or factory floor reduces pull times and saves WAN bandwidth.
- **Harbor proxy cache** — Harbor's built-in proxy cache feature lets you centralise image storage and apply access-control policies across registries.

---

## How It Works in KubeSolo

KubeSolo manages containerd's registry configuration using the [hosts.toml](https://github.com/containerd/containerd/blob/main/docs/hosts.md) mechanism introduced in containerd v1.5.

Two things happen at startup:

1. **`config_path` is always set.** KubeSolo unconditionally sets containerd's `config_path` to `/etc/containerd/certs.d`. This tells containerd to look in that directory for per-registry `hosts.toml` files.

2. **`hosts.toml` files are written on demand.** When `--registry-mirror` flags are present, KubeSolo writes a `hosts.toml` file for each configured upstream into `/etc/containerd/certs.d/<upstream>/hosts.toml` before containerd starts.

KubeSolo only writes files for registries it is explicitly told to manage. If you have manually created a `hosts.toml` for a registry (for example, to add custom CA certificates or credentials), KubeSolo will never touch it as long as that registry is not passed via `--registry-mirror`.

---

## Using `--registry-mirror`

### Flag format

```
--registry-mirror=UPSTREAM=MIRROR_URL
```

`UPSTREAM` is the registry hostname as it appears in image references (e.g. `docker.io`, `ghcr.io`). `MIRROR_URL` is the full OCI API base URL — the part that comes before the image path — including scheme and any path prefix.

---

### Plain pull-through proxy

Works with Sonatype Nexus, JFrog Artifactory, and any OCI-compliant proxy that exposes the registry API at the root path:

```bash
kubesolo --registry-mirror=docker.io=https://mirror.corp
```

Generated `/etc/containerd/certs.d/docker.io/hosts.toml`:

```toml
# Managed by kubesolo — overwritten on every restart. Remove this line to manage manually.
server = "https://registry-1.docker.io"

[host."https://mirror.corp"]
  capabilities = ["pull", "resolve"]
```

---

### Harbor proxy cache project

Harbor's registry API is project-scoped — requests are served under `/v2/<project>/`. The `/v2/<project>` segment must therefore be included in the mirror URL:

```bash
kubesolo --registry-mirror=docker.io=https://harbor.corp/v2/docker.io
```

Generated `/etc/containerd/certs.d/docker.io/hosts.toml`:

```toml
# Managed by kubesolo — overwritten on every restart. Remove this line to manage manually.
server = "https://registry-1.docker.io"

[host."https://harbor.corp/v2/docker.io"]
  capabilities = ["pull", "resolve"]
  override_path = true
```

> `override_path = true` is set automatically whenever the mirror URL contains a non-root path. This instructs containerd to use the mirror URL path as-is rather than appending the standard `/v2/` prefix.

---

### Catch-all mirror (`_default`)

Use `_default` as the upstream to apply a mirror to any registry that does not have an explicit entry:

```bash
kubesolo --registry-mirror=_default=https://airgap.internal
```

Generated `/etc/containerd/certs.d/_default/hosts.toml`:

```toml
# Managed by kubesolo — overwritten on every restart. Remove this line to manage manually.

[host."https://airgap.internal"]
  capabilities = ["pull", "resolve"]
```

> The `server` line is omitted for `_default` because there is no single upstream to fall back to.

---

### Multiple mirrors

The flag is repeatable:

```bash
kubesolo \
  --registry-mirror=docker.io=https://harbor.corp/v2/docker.io \
  --registry-mirror=ghcr.io=https://harbor.corp/v2/ghcr.io \
  --registry-mirror=registry.k8s.io=https://harbor.corp/v2/registry.k8s.io
```

---

### Via `install.sh`

```bash
sudo bash install.sh \
  --bin-path=./kubesolo \
  --registry-mirror=docker.io=https://harbor.corp/v2/docker.io
```

---

### Via environment variable

```bash
export KUBESOLO_REGISTRY_MIRRORS="docker.io=https://harbor.corp/v2/docker.io"
kubesolo
```

> When configuring multiple mirrors via the environment variable, use a comma-separated list:
> ```bash
> export KUBESOLO_REGISTRY_MIRRORS="docker.io=https://harbor.corp/v2/docker.io,ghcr.io=https://harbor.corp/v2/ghcr.io"
> ```

---

## Advanced — Manual `hosts.toml` Management

For cases that require authentication, custom CA certificates, TLS verification skipping, or multiple fallback mirrors, manage the `hosts.toml` file directly.

KubeSolo uses a marker-based ownership model to decide whether to write a file on startup:

- Files generated by KubeSolo begin with `# Managed by kubesolo ...`. KubeSolo will overwrite these on every restart.
- If that first line is absent, KubeSolo leaves the file untouched regardless of whether `--registry-mirror` is passed for that registry.

**Taking manual ownership of a registry KubeSolo currently manages:**

1. Open `/etc/containerd/certs.d/<upstream>/hosts.toml`
2. Remove the first line (`# Managed by kubesolo ...`)
3. Make your customisations and save

On the next restart KubeSolo will detect the missing marker and skip the file. The `--registry-mirror` flag for that registry can be left in place or removed — either way, the file will not be overwritten.

Example path: `/etc/containerd/certs.d/docker.io/hosts.toml`

A full example with a custom CA and credentials:

```toml
server = "https://registry-1.docker.io"

[host."https://harbor.corp/v2/docker.io"]
  capabilities = ["pull", "resolve"]
  override_path = true
  ca = ["/etc/ssl/certs/harbor-ca.crt"]

[host."https://harbor.corp/v2/docker.io".header]
  Authorization = ["Basic <base64-encoded-credentials>"]
```

Containerd reads `hosts.toml` files at pull time — no restart is required after editing the file.

---

## Supported Upstream Registries

KubeSolo knows the canonical upstream server URL for these registries:

| Upstream | Upstream server URL |
|---|---|
| `docker.io` | `https://registry-1.docker.io` |
| `ghcr.io` | `https://ghcr.io` |
| `gcr.io` | `https://gcr.io` |
| `quay.io` | `https://quay.io` |
| `registry.k8s.io` | `https://registry.k8s.io` |
| `k8s.gcr.io` | `https://k8s.gcr.io` |

For any other upstream, KubeSolo falls back to `https://<upstream>` as the server URL. You can always override this by managing the `hosts.toml` manually.

---

## Deferred / Future

The following scenarios are not yet configurable via `--registry-mirror` and require manual `hosts.toml` management instead:

| Feature | Workaround |
|---|---|
| Custom CA certificate | Set `ca = ["/path/to/ca.crt"]` in `hosts.toml` |
| Skip TLS verification | Set `skip_verify = true` in `hosts.toml` |
| Mirror credentials / auth headers | Set `[host."...".header]` in `hosts.toml` |
| Multiple fallback mirrors per registry | Add multiple `[host."..."]` sections in `hosts.toml` |

See [Advanced — Manual `hosts.toml` Management](#advanced--manual-hoststoml-management) above for an example.
