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

> **Architecture support:** the `portainer/d2k` container image is currently published only for `linux/amd64` and `linux/arm64`. On `arm` and `riscv64` builds, passing `--d2k` logs a warning at startup and the flag is silently cleared — no PKI material is generated, no image is imported, and no Kubernetes resources are created.

> **Load-balancer requirement:** `--d2k` requires `--load-balancer` (the default). The d2k Service endpoint is populated by the KubeSolo LoadBalancer webhook; without it the endpoint address is never resolved. KubeSolo will exit at startup if `--d2k=true` is combined with `--load-balancer=false`.

> **Namespace is fixed after first start:** the d2k server certificate embeds the in-cluster DNS names for the namespace chosen at first start (e.g. `d2k.workloads.svc.cluster.local`). Changing `--d2k-namespace` on a subsequent start reuses the existing certificate, whose SANs no longer match — clients that connect via in-cluster DNS will fail TLS verification. To change the namespace, delete `/var/lib/kubesolo/pki/d2k/server.crt` and `/var/lib/kubesolo/pki/d2k/server.key` before restarting so the certificate is regenerated for the new namespace.

---

## Connecting

The client certificates are generated on the KubeSolo node under `/var/lib/kubesolo/pki/d2k/`.

| Path | Purpose |
|---|---|
| `/var/lib/kubesolo/pki/ca/ca.crt` | CA certificate |
| `/var/lib/kubesolo/pki/d2k/client.crt` | Client certificate |
| `/var/lib/kubesolo/pki/d2k/client.key` | Client key |

To find the node IP:

```bash
export KUBECONFIG=/var/lib/kubesolo/pki/admin/admin.kubeconfig
kubectl get nodes -o wide
```

### Copying certificates to your local machine

```bash
CERT_DIR="${HOME}/.config/d2k"
mkdir -p "${CERT_DIR}"
scp user@<NODE_IP>:/var/lib/kubesolo/pki/ca/ca.crt          "${CERT_DIR}/ca.crt"
scp user@<NODE_IP>:/var/lib/kubesolo/pki/d2k/client.crt     "${CERT_DIR}/client.crt"
scp user@<NODE_IP>:/var/lib/kubesolo/pki/d2k/client.key     "${CERT_DIR}/client.key"
```

### Creating a Docker context

Once the certificates are available locally, create a named Docker context:

```bash
CERT_DIR="${HOME}/.config/d2k"
docker context create d2k \
  --docker "host=tcp://<NODE_IP>:2376,ca=${CERT_DIR}/ca.crt,cert=${CERT_DIR}/client.crt,key=${CERT_DIR}/client.key"
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
  --tlscacert "${CERT_DIR}/ca.crt" \
  --tlscert   "${CERT_DIR}/client.crt" \
  --tlskey    "${CERT_DIR}/client.key" \
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
