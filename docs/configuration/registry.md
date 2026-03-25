# Registry Configuration

KubeSolo supports container registry configuration — including mirrors, pull-through proxy caches, private registries, and custom TLS — as an advanced capability using containerd's [`hosts.toml`](https://github.com/containerd/containerd/blob/main/docs/hosts.md) mechanism.

No flags or code changes are required. Users who need registry configuration create the appropriate files in a well-known directory; users who do not are unaffected.

---

## How It Works

At startup, KubeSolo sets containerd's `config_path` to:

```
/var/lib/kubesolo/containerd/registry
```

This tells containerd to look in that directory for per-registry configuration files at image pull time. When the directory is absent or empty, containerd behaves exactly as it does by default.

To configure a registry, create a subdirectory named after the registry hostname and place a `hosts.toml` file inside it:

```
/var/lib/kubesolo/containerd/registry/
└── <registry-hostname>/
    └── hosts.toml
```

containerd reads `hosts.toml` files at pull time — **no restart is needed** after creating or modifying them. A KubeSolo restart is only required the first time, to pick up the `config_path` setting in the generated containerd config.

> **Note:** Always use fully-qualified image references (e.g. `docker.io/library/nginx:alpine`) so containerd can unambiguously map the pull to the correct `hosts.toml`.

---

## Scenarios

### Simple mirror for Docker Hub

```bash
mkdir -p /var/lib/kubesolo/containerd/registry/docker.io
```

```toml
# /var/lib/kubesolo/containerd/registry/docker.io/hosts.toml
server = "https://registry-1.docker.io"

[host."https://mirror.corp.internal"]
  capabilities = ["pull", "resolve"]
```

If the mirror does not have the image, containerd falls back to `registry-1.docker.io`.

---

### Harbor proxy cache

Harbor's registry API is project-scoped — the project name sits inside the API path after `/v2/`. The mirror URL must include this prefix, and `override_path = true` must be set so containerd does not prepend its own `/v2/`.

```bash
mkdir -p /var/lib/kubesolo/containerd/registry/docker.io
```

```toml
# /var/lib/kubesolo/containerd/registry/docker.io/hosts.toml
server = "https://registry-1.docker.io"

[host."https://harbor.corp.internal/v2/docker.io"]
  capabilities = ["pull", "resolve"]
  override_path = true
```

For a Harbor instance with a private CA:

```toml
server = "https://registry-1.docker.io"

[host."https://harbor.corp.internal/v2/docker.io"]
  capabilities = ["pull", "resolve"]
  override_path = true
  ca = "/etc/ssl/certs/harbor-ca.crt"
```

For a private Harbor project requiring authentication:

```toml
server = "https://registry-1.docker.io"

[host."https://harbor.corp.internal/v2/docker.io"]
  capabilities = ["pull", "resolve"]
  override_path = true
  [host."https://harbor.corp.internal/v2/docker.io".header]
    Authorization = ["Basic <base64(robot$account:token)>"]
```

Generate the Base64 value with:

```bash
echo -n "robot\$account:token" | base64
```

---

### Multiple registries

Create a separate subdirectory for each upstream registry:

```
/var/lib/kubesolo/containerd/registry/
├── docker.io/
│   └── hosts.toml
├── ghcr.io/
│   └── hosts.toml
└── quay.io/
    └── hosts.toml
```

```toml
# ghcr.io/hosts.toml
server = "https://ghcr.io"

[host."https://harbor.corp.internal/v2/ghcr.io"]
  capabilities = ["pull", "resolve"]
  override_path = true
```

---

### Air-gapped environment

Use the special `_default` directory as a catch-all for any registry that does not have its own entry:

```bash
mkdir -p /var/lib/kubesolo/containerd/registry/_default
```

```toml
# /var/lib/kubesolo/containerd/registry/_default/hosts.toml
[host."https://airgap-registry.corp.internal"]
  capabilities = ["pull", "resolve"]
  ca = "/etc/ssl/certs/corp-ca.crt"
```

A registry-specific `hosts.toml` always takes precedence over `_default`.

---

### Local registry without TLS

```bash
mkdir -p /var/lib/kubesolo/containerd/registry/localhost:5000
```

```toml
# /var/lib/kubesolo/containerd/registry/localhost:5000/hosts.toml
server = "http://localhost:5000"

[host."http://localhost:5000"]
  capabilities = ["pull", "resolve", "push"]
  skip_verify = true
```

---

## Verifying

After placing your `hosts.toml` files, run a test pod using an image not already cached locally and confirm the pull appears in your mirror or proxy cache logs:

```bash
kubectl run registry-test --image=docker.io/library/nginx:alpine --restart=Never \
  --kubeconfig=/var/lib/kubesolo/pki/admin/admin.kubeconfig

kubectl delete pod registry-test \
  --kubeconfig=/var/lib/kubesolo/pki/admin/admin.kubeconfig
```
