# d2k (Docker-to-Kubernetes API)

KubeSolo can embed [d2k](https://github.com/portainer/d2k), the Portainer Docker-to-Kubernetes API translator, so a single KubeSolo node exposes a Docker-compatible API endpoint that translates Docker calls into Kubernetes resources scoped to one namespace. With d2k enabled, existing Docker tooling, scripts, and CI pipelines can target a KubeSolo node without manual YAML application or a separate translator deployment.

The integration is gated behind a flag and is **off by default**. Enabling it does not change any other KubeSolo behaviour.

---

## Enabling d2k

Two flags drive the integration:

| Flag | Env var | Default |
|---|---|---|
| `--d2k` | `KUBESOLO_D2K` | `false` |
| `--d2k-namespace` | `KUBESOLO_D2K_NAMESPACE` | `d2k` |

When `--d2k` is set, KubeSolo:

1. Reuses the existing kubesolo CA at `/var/lib/kubesolo/pki/ca/` to mint a d2k server certificate and a d2k client certificate. Both are persisted under `/var/lib/kubesolo/pki/d2k/` and reused on subsequent starts.
2. Reconciles all required Kubernetes resources programmatically (Namespace, ServiceAccount, Role/RoleBinding, ClusterRole/ClusterRoleBinding, `d2k-tls` Secret of type `kubernetes.io/tls`, Deployment, and a LoadBalancer Service on port `2376`).
3. Waits for the LoadBalancer Service to receive an external IP.

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --d2k=true --d2k-namespace=workloads
```

> **Architecture support:** the `portainer/d2k` container image is currently published only for `linux/amd64` and `linux/arm64`. On `arm` and `riscv64` builds, passing `--d2k` logs a warning and skips the deployment — no resources are created and no error is returned.

---

## Connecting

The client certificates are generated on the KubeSolo node under `/var/lib/kubesolo/pki/d2k/`. KubeSolo also writes `ca.pem`, `cert.pem`, and `key.pem` symlinks in `/var/lib/kubesolo/d2k/` pointing at the CA and client material, using the naming convention expected by the Docker CLI.

| Path | Purpose |
|---|---|
| `/var/lib/kubesolo/pki/ca/ca.crt` | CA certificate |
| `/var/lib/kubesolo/pki/d2k/client.crt` | Client certificate |
| `/var/lib/kubesolo/pki/d2k/client.key` | Client key |
| `/var/lib/kubesolo/d2k/ca.pem` | Symlink → CA certificate |
| `/var/lib/kubesolo/d2k/cert.pem` | Symlink → client certificate |
| `/var/lib/kubesolo/d2k/key.pem` | Symlink → client key |

To find the node IP:

```bash
export KUBECONFIG=/var/lib/kubesolo/pki/admin/admin.kubeconfig
kubectl get nodes -o wide
```

### Copying certificates to your local machine

**Via SCP:**

```bash
CERT_DIR="${HOME}/.config/d2k"
mkdir -p "${CERT_DIR}"
scp user@<NODE_IP>:/var/lib/kubesolo/d2k/ca.pem   "${CERT_DIR}/ca.pem"
scp user@<NODE_IP>:/var/lib/kubesolo/d2k/cert.pem "${CERT_DIR}/cert.pem"
scp user@<NODE_IP>:/var/lib/kubesolo/d2k/key.pem  "${CERT_DIR}/key.pem"
```

Or in a single command if your shell supports brace expansion over SSH:

```bash
CERT_DIR="${HOME}/.config/d2k"
mkdir -p "${CERT_DIR}"
scp "user@<NODE_IP>:/var/lib/kubesolo/d2k/{ca.pem,cert.pem,key.pem}" "${CERT_DIR}/"
```

**On the node directly** (e.g., running Docker on the same host as KubeSolo):

Use the paths under `/var/lib/kubesolo/d2k/` directly — no copy needed.

### Creating a Docker context

Once the certificates are available locally, create a named Docker context:

```bash
CERT_DIR="${HOME}/.config/d2k"
docker context create d2k \
  --docker "host=tcp://<NODE_IP>:2376,ca=${CERT_DIR}/ca.pem,cert=${CERT_DIR}/cert.pem,key=${CERT_DIR}/key.pem"
```

Switch to it and start issuing Docker commands:

```bash
docker context use d2k
docker ps
```

### Explicit flags (without a context)

```bash
CERT_DIR="${HOME}/.config/d2k"
docker -H tcp://<NODE_IP>:2376 \
  --tlsverify \
  --tlscacert "${CERT_DIR}/ca.pem" \
  --tlscert   "${CERT_DIR}/cert.pem" \
  --tlskey    "${CERT_DIR}/key.pem" \
  ps
```

`docker ps` lists pods in the `--d2k-namespace` namespace as Docker containers. See [the upstream d2k README](https://github.com/portainer/d2k) for the full Docker-to-Kubernetes translation table.

---

## Verifying

After starting KubeSolo with `--d2k=true`, confirm the resources have been reconciled:

```bash
export KUBECONFIG=/var/lib/kubesolo/pki/admin/admin.kubeconfig
kubectl -n <namespace> get deploy,svc,sa,role,rolebinding,secret/d2k-tls
kubectl get clusterrole,clusterrolebinding | grep d2k-node-reader
```

---

## Out of scope

The integration intentionally limits itself to a single, mTLS-protected, Swarm-mode-enabled d2k deployment per KubeSolo node:

- **Multi-namespace translation.** d2k always translates against the namespace passed via `--d2k-namespace`.
- **Authn/authz beyond mTLS.** The endpoint is protected by the kubesolo-CA-signed client certificate; no additional bearer-token, OIDC, or RBAC layer is added on top.
- **Custom d2k image overrides.** The image is pinned to the version baked into the KubeSolo binary. Use `crane` and `containerd` directly if you need to evaluate a different upstream tag.
