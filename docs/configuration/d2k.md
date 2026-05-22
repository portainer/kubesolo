# d2k (Docker-to-Kubernetes API)

KubeSolo can embed [d2k](https://github.com/portainer/d2k), the Portainer Docker-to-Kubernetes API translator, so a single KubeSolo node exposes a Docker-compatible API endpoint that translates Docker calls into Kubernetes resources scoped to one namespace. With d2k enabled, existing Docker tooling, scripts, and CI pipelines can target a KubeSolo node without manual YAML application or a separate translator deployment.

The integration is gated behind a flag and is **off by default**. Enabling it does not change any other KubeSolo behaviour.

---

## Enabling d2k

Two flags drive the integration:

| Flag | Env var | Default |
|---|---|---|
| `--d2k` | `KUBESOLO_D2K` | `false` |
| `--d2k-namespace` | `KUBESOLO_D2K_NAMESPACE` | `default` |

When `--d2k` is set, KubeSolo:

1. Reuses the existing kubesolo CA at `/var/lib/kubesolo/pki/ca/` to mint a d2k server certificate and a d2k client certificate. Both are persisted under `/var/lib/kubesolo/pki/d2k/` and reused on subsequent starts.
2. Reconciles all required Kubernetes resources programmatically (Namespace, ServiceAccount, Role/RoleBinding, ClusterRole/ClusterRoleBinding, `d2k-tls` Secret of type `kubernetes.io/tls`, Deployment, and a LoadBalancer Service on port `2376`).
3. Waits for the LoadBalancer Service to receive an external IP, then writes connection details to `/var/lib/kubesolo/d2k/connection.env` and `/var/lib/kubesolo/d2k/connection.txt`.

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --d2k=true --d2k-namespace=workloads
```

> **Architecture support:** the `portainer/d2k` container image is currently published only for `linux/amd64` and `linux/arm64`. `--d2k` is a no-op on `arm` and `riscv64` builds.

---

## Connecting

The on-disk layout is **static across machines and restarts**. The only value that changes between hosts is the node IP encoded in `DOCKER_HOST`.

| Path | Purpose |
|---|---|
| `/var/lib/kubesolo/pki/ca/ca.crt` | CA certificate (`docker --tlscacert`) |
| `/var/lib/kubesolo/pki/d2k/server.crt` | d2k server certificate (mounted into the pod) |
| `/var/lib/kubesolo/pki/d2k/server.key` | d2k server key (mounted into the pod) |
| `/var/lib/kubesolo/pki/d2k/client.crt` | Client certificate (`docker --tlscert`) |
| `/var/lib/kubesolo/pki/d2k/client.key` | Client key (`docker --tlskey`) |
| `/var/lib/kubesolo/d2k/connection.env` | Shell-sourceable env vars (regenerated on every start) |
| `/var/lib/kubesolo/d2k/connection.txt` | Human-readable copy/paste block |

KubeSolo also creates `ca.pem`, `cert.pem`, and `key.pem` symlinks alongside the client material so `DOCKER_CERT_PATH=/var/lib/kubesolo/pki/d2k` works directly with the docker CLI.

### Quick start

Source the env file and run docker against the node:

```bash
source /var/lib/kubesolo/d2k/connection.env
docker ps
```

### Explicit form (no env vars)

```bash
docker -H tcp://<NODE_IP>:2376 \
  --tlsverify \
  --tlscacert /var/lib/kubesolo/pki/ca/ca.crt \
  --tlscert /var/lib/kubesolo/pki/d2k/client.crt \
  --tlskey /var/lib/kubesolo/pki/d2k/client.key \
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

Then inspect the persisted connection file:

```bash
cat /var/lib/kubesolo/d2k/connection.txt
```

---

## Out of scope

The integration intentionally limits itself to a single, mTLS-protected, Swarm-mode-enabled d2k deployment per KubeSolo node:

- **Multi-namespace translation.** d2k always translates against the namespace passed via `--d2k-namespace`.
- **Authn/authz beyond mTLS.** The endpoint is protected by the kubesolo-CA-signed client certificate; no additional bearer-token, OIDC, or RBAC layer is added on top.
- **Custom d2k image overrides.** The image is pinned to the version baked into the KubeSolo binary. Use `crane` and `containerd` directly if you need to evaluate a different upstream tag.
