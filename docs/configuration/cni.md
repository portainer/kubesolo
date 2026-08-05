# Container Network Interface (CNI)

KubeSolo ships with a built-in bridge CNI so a single node works out of the box. If you want a feature-rich CNI — NetworkPolicy enforcement, eBPF dataplane, observability — you can install one on top. This guide covers **Cilium**; the KubeSolo-specific notes at the top apply to any external CNI.

---

## Embedded bridge CNI: MTU auto-detection

The embedded bridge CNI auto-detects the MTU of the host's primary network interface and applies it to the `cni0` bridge and pod veth interfaces, so pod traffic doesn't silently fragment on hosts with a reduced MTU (VPN/tunnel/PPPoE links, some cloud secondary NICs). Override auto-detection with `--mtu` if needed — see [`--mtu`](../installation/flags.md#--mtu) in the flags reference.

---

## Two things every external CNI must account for

KubeSolo differs from a stock Kubernetes node in two ways that affect CNI installation.

### 1. Custom CNI plugin directory

KubeSolo's containerd searches a **non-standard** directory for CNI plugin binaries:

| containerd setting | KubeSolo path | Stock default |
|---|---|---|
| Plugin binaries (`bin_dirs`) | `/var/lib/kubesolo/containerd/cni/plugins` | `/opt/cni/bin` |
| CNI config (`conf_dir`) | `/etc/cni/net.d` | `/etc/cni/net.d` |

The CNI **config** directory is the standard `/etc/cni/net.d`, so no override is needed there. But any CNI that drops its plugin binary into the default `/opt/cni/bin` will not be found, and pod sandbox creation fails with:

```
plugin type="cilium-cni" failed (add): failed to find plugin "cilium-cni" in path [/var/lib/kubesolo/containerd/cni/plugins]
```

The fix is to point the CNI's binary install path at KubeSolo's plugins directory.

### 2. No scheduler — set replicas to 1

KubeSolo has **no kube-scheduler**. A mutating webhook assigns every pod to the single node by patching `spec.nodeName` directly. This bypasses pod anti-affinity and the `PodFitsHostPorts` scheduling predicate, so any workload that expects the scheduler to spread replicas across nodes (or that uses host ports) will stack all its replicas onto the one node. Extra replicas that bind the same host port are rejected by the kubelet with the status **`NodePorts`**:

```
kube-system   cilium-operator-...-6b2mr   0/1   NodePorts   0   102s
kube-system   cilium-operator-...-jm9vp   1/1   Running     0   102s
```

For components like `cilium-operator` that default to multiple replicas, set the replica count to `1`.

---

## Cilium

Tested with Cilium `1.19.5` installed via the [Cilium CLI](https://docs.cilium.io/en/stable/gettingstarted/k8s-install-default/).

> **Resource requirements:** Cilium is heavy. KubeSolo will not run it on a 512MB device — the Cilium agent, Envoy, and operator alone exceed that budget. Run on a beefier machine for the best outcome.

### Install

```bash
cilium install --version 1.19.5 \
  --set cni.binPath=/var/lib/kubesolo/containerd/cni/plugins \
  --set operator.replicas=1
```

| Flag | Why it's needed on KubeSolo |
|---|---|
| `cni.binPath=/var/lib/kubesolo/containerd/cni/plugins` | Installs the `cilium-cni` binary where KubeSolo's containerd looks for it, instead of the default `/opt/cni/bin`. |
| `operator.replicas=1` | Single node has no scheduler; extra operator replicas land on the same node and fail the host-port check with status `NodePorts`. |

`cni.confPath` stays at its default (`/etc/cni/net.d`), which already matches KubeSolo.

### Applying the settings to an existing install

If Cilium is already installed, apply the same settings without reinstalling, then restart the agent so the binary is (re)copied:

```bash
cilium upgrade --reuse-values \
  --set cni.binPath=/var/lib/kubesolo/containerd/cni/plugins \
  --set operator.replicas=1

kubectl -n kube-system rollout restart ds/cilium
```

### Verify

```bash
cilium status --wait
kubectl -n kube-system get pods -l k8s-app=cilium
ls -l /var/lib/kubesolo/containerd/cni/plugins/cilium-cni   # binary in the right place
```

Expected: the `cilium` agent `1/1 Running`, exactly one `cilium-operator 1/1 Running`, and no pods stuck in `NodePorts`.

If earlier attempts left behind failed operator pods, clear them:

```bash
kubectl -n kube-system delete pod -l io.cilium/app=operator --field-selector status.phase=Failed
```

---

## Kubeconfig note

If you hit `x509: certificate signed by unknown authority` when a CNI tool checks cluster reachability, the kubeconfig you are using likely carries a stale CA — KubeSolo regenerates its PKI (including the CA) when the node IP changes. Refresh it from the running node:

```bash
kubesoloctl kubeconfig fetch          # merges the current kubeconfig into ~/.kube/config
```
