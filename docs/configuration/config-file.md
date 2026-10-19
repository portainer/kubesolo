# Configuration File

KubeSolo reads its settings from a single YAML file, by default
`/etc/kubesolo/config.yaml`.

The file exists because the alternative did not scale. Every setting used to be a
command-line flag, and each new one had to be added in nine places across the
binary, `kubesoloctl`, the install script and the documentation. The file is one
schema, one loader, and one place to add a setting.

---

## When changes take effect

**On restart.** KubeSolo reads its configuration once during startup and derives
every path, certificate and component argument from it. There is no live reload:
a change written here is desired state, and takes effect the next time KubeSolo
starts.

```bash
sudo systemctl restart kubesolo
```

Four settings cannot be changed at all on an existing install. `path`: every
certificate, the cluster database and all container state live below it, and
nothing moves them. `kubernetes.podCIDR`, `kubernetes.serviceCIDR` and
`kubernetes.clusterDNS`: the node's pod range, every Service's cluster IP and the
API server certificate were allocated from them. Choose them before the first
start.

### The Portainer agent's settings are set once

`portainer.edgeID`, `portainer.edgeKey` and `portainer.async` can be set, and
corrected, until KubeSolo has deployed a Portainer agent from them, which is how
an agent is added after install. Once KubeSolo has started with both
`portainer.edgeID` and `portainer.edgeKey` set, none of the three can change,
and that includes clearing them.

KubeSolo writes them into the agent's `portainer-agent-edge` ConfigMap and
`portainer-agent-edge-key` Secret when it first deploys the agent, and never
updates those afterwards. Portainer manages them from then on. A changed value
would be saved in this file but never reach the agent, so it is refused instead:

- the [configuration API](config-api.md) answers `409`, naming the setting;
- `kubesoloctl config set` and `kubesoloctl config edit` refuse to save it;
- a hand edit is not prevented, but `POST /api/v1/restart` then answers `409`
  rather than restart KubeSolo while ignoring it.

Changing these on an existing agent is not supported. `portainer.image` is not
affected: a change to it is applied when KubeSolo restarts.

Removing them by hand and restarting KubeSolo does not remove the agent:
KubeSolo stops managing it, and its Deployment stays. If that agent is not
running, KubeSolo's log names it as left from an earlier configuration. Put the
settings back, or delete it with
`kubectl -n portainer delete deployment portainer-agent`.

---

## Precedence

Four layers, lowest to highest:

| Layer | Notes |
|---|---|
| Built-in defaults | What KubeSolo runs with when nothing is set |
| The configuration file | This file |
| Environment variables | `KUBESOLO_*`, one per setting |
| Command-line flags | Deprecated, but still win |

Each layer only overrides the settings it actually specifies, so a file that sets
one value leaves the rest at their defaults.

**Flags still outrank the file.** They are retained so that existing installs keep
working, but they are deprecated and no new ones will be added. If a setting you
changed in the file appears not to take effect, check whether a flag in the
service definition is overriding it:

```bash
grep -- '--' /etc/systemd/system/kubesolo.service
```

`kubesoloctl upgrade` converts a flag-based service definition into a
configuration file automatically, so this only affects installs that have not
been upgraded since the file was introduced.

---

## Editing

### With `kubesoloctl`

```bash
# Show everything
kubesoloctl config get

# Show one setting
kubesoloctl config get network.nodeIP

# Change one setting
sudo kubesoloctl config set network.nodeIP 10.0.0.5

# Open the whole file in $EDITOR, validated before it is saved
sudo kubesoloctl config edit

# Check a candidate file without installing it
kubesoloctl config validate -f ./candidate.yaml

# List every setting, its type and its default
kubesoloctl config schema
```

`kubesoloctl` prints YAML throughout, matching the configuration file itself.
The [configuration API](config-api.md) serves the same content as JSON, which is
the convention for an HTTP API.

Nothing is written until validation passes, so a rejected value leaves the
existing configuration exactly as it was.

When KubeSolo is running with its configuration API enabled, `kubesoloctl` goes
through that instead of editing the file underneath the running process. It says
which it used.

### By hand

The file is plain YAML. Two things to know:

- **Comments are not preserved** when KubeSolo or `kubesoloctl` writes the file.
  A write re-serialises the document, and comments and key order are lost. The
  previous version is kept as `/etc/kubesolo/config.yaml.bak`.
- **Keys are written in alphabetical order.** Any order parses; that is simply
  what the serialiser produces.

Always check by hand edits before restarting:

```bash
kubesoloctl config validate
```

---

## Migrating from flags

Run the binary with the flags your service definition currently passes, plus
`--print-config`. It resolves them exactly as it would at startup and prints the
equivalent document, without starting anything:

```bash
# What the current service definition passes
grep '^ExecStart=' /etc/systemd/system/kubesolo.service

# Convert it
sudo /usr/local/bin/kubesolo <those flags> --print-config | sudo tee /etc/kubesolo/config.yaml
sudo chmod 600 /etc/kubesolo/config.yaml
```

Then reduce the service definition to a single flag:

```
ExecStart=/usr/local/bin/kubesolo --config=/etc/kubesolo/config.yaml
```

`kubesoloctl upgrade` does all of this for you, keeping the previous service
definition as `.bak`.

---

## Permissions

The file is written `0600` and owned by root. It carries
`portainer.edgeKey`, which is a credential.

---

## The whole document

Every setting at its default. A real configuration only needs the keys it
overrides — anything omitted falls back to the default.

```yaml
apiVersion: kubesolo.io/v1alpha1
kind: Config

path: /var/lib/kubesolo

kubernetes:
  nodeName: ""            # empty = the hostname
  podCIDR: 10.42.0.0/16   # immutable
  serviceCIDR: 10.43.0.0/16 # immutable; its first address is the kubernetes Service's
  clusterDNS: ""          # immutable; empty = the tenth address of serviceCIDR (10.43.0.10)
  apiServer:
    extraSANs: []
    startupTimeoutSeconds: 600
    port: 6443
  controllerManager:
    port: 10257
  kubelet:
    cpuManager:
      policy: none        # none | static
      policyOptions: {}
      reservedCPUs: ""    # e.g. "0-1"; defaults to "0" under the static policy
    systemReserved: {}    # e.g. {cpu: "1", memory: 500Mi}
    port: 10250
    healthzPort: 10248    # on loopback
  kubeProxy:
    healthzPort: 10256
  webhook:
    port: 10443           # on loopback; the webhook pins every pod to the node

network:
  nodeIP: ""              # empty = auto-detect, preferring a private (RFC 1918) address
  mtu: 0                  # 0 = auto-detect
  disableIPv6: false
  loadBalancer:
    enabled: true
    ip: ""                # empty = use nodeIP

runtime:
  endpoint: ""            # empty = run the embedded containerd
  containerMode: null     # null = auto-detect; true/false to force

storage:
  localPath:
    enabled: true
    sharedPath: ""
  kine:
    port: 2379            # on loopback
  dbWALRepair: false

logging:
  debug: false
  pprof: false
  pprofPort: 6060

metrics:
  enabled: false
  bindAddress: "127.0.0.1:9105"

api:
  enabled: false
  socketPath: ""          # empty = <path>/config.sock

portainer:
  edgeID: ""
  edgeKey: ""
  async: false
  image: "docker.io/portainer/agent:lts"

d2k:
  enabled: false
  namespace: d2k
  port: 2376              # the Docker API's port at the LoadBalancer IP
```

Only `kubernetes:` groups its contents, because that family grows: the API
server, the controller manager, the kubelet, kube-proxy, the webhook and the
cluster networks each have their own. Everything else is single-purpose and stays
at the top level.

Every port KubeSolo listens on is a setting, so a host that already uses one — a
web UI on 10443, another cluster's etcd on 2379 — only needs that one moved. Two
of KubeSolo's own on the same port are refused before anything starts.

---

## Every setting

| Setting | Type | Default | |
|---|---|---|---|
| `api.enabled` | `boolean` | `false` |
| `api.socketPath` | `string` | `""` |
| `d2k.enabled` | `boolean` | `false` |
| `d2k.namespace` | `string` | `d2k` |
| `d2k.port` | `integer` | `2376` |
| `kubernetes.apiServer.extraSANs` | `array` | `[]` |
| `kubernetes.apiServer.port` | `integer` | `6443` |
| `kubernetes.apiServer.startupTimeoutSeconds` | `integer` | `600` |
| `kubernetes.clusterDNS` | `string` | `""` | **immutable**
| `kubernetes.controllerManager.port` | `integer` | `10257` |
| `kubernetes.kubeProxy.healthzPort` | `integer` | `10256` |
| `kubernetes.kubelet.cpuManager.policy` | `string` | `none` |
| `kubernetes.kubelet.cpuManager.policyOptions` | `object` | `map[]` |
| `kubernetes.kubelet.cpuManager.reservedCPUs` | `string` | `""` |
| `kubernetes.kubelet.healthzPort` | `integer` | `10248` |
| `kubernetes.kubelet.port` | `integer` | `10250` |
| `kubernetes.kubelet.systemReserved` | `object` | `map[]` |
| `kubernetes.nodeName` | `string` | `""` |
| `kubernetes.podCIDR` | `string` | `10.42.0.0/16` | **immutable**
| `kubernetes.serviceCIDR` | `string` | `10.43.0.0/16` | **immutable**
| `kubernetes.webhook.port` | `integer` | `10443` |
| `logging.debug` | `boolean` | `false` |
| `logging.pprof` | `boolean` | `false` |
| `logging.pprofPort` | `integer` | `6060` |
| `metrics.bindAddress` | `string` | `127.0.0.1:9105` |
| `metrics.enabled` | `boolean` | `false` |
| `network.disableIPv6` | `boolean` | `false` |
| `network.loadBalancer.enabled` | `boolean` | `true` |
| `network.loadBalancer.ip` | `string` | `""` |
| `network.mtu` | `integer` | `0` |
| `network.nodeIP` | `string` | `""` |
| `path` | `string` | `/var/lib/kubesolo` | **immutable**
| `portainer.async` | `boolean` | `false` | **set-once**
| `portainer.edgeID` | `string` | `""` | **set-once**
| `portainer.edgeKey` | `string` | `—` | *(secret)*, **set-once**
| `portainer.image` | `string` | `docker.io/portainer/agent:lts` |
| `runtime.containerMode` | `boolean` | `<nil>` |
| `runtime.endpoint` | `string` | `""` |
| `storage.dbWALRepair` | `boolean` | `false` |
| `storage.kine.port` | `integer` | `2379` |
| `storage.localPath.enabled` | `boolean` | `true` |
| `storage.localPath.sharedPath` | `string` | `""` |

**set-once** settings can be set until a Portainer agent is deployed, and not
changed after; see [above](#the-portainer-agents-settings-are-set-once).

`runtime.containerMode` is deliberately three-state: unset means auto-detect,
while `true` and `false` force the answer. That is why its default is shown as
absent rather than `false`.

---

## Flag and environment variable equivalents

Every flag below still works and still overrides the file. They are deprecated:
no new flags will be added, and new settings are configurable through the file
(and their environment variable) only.

| Flag | Environment variable | Setting |
|---|---|---|
| `--d2k` | `KUBESOLO_D2K` | `d2k.enabled` |
| `--d2k-namespace` | `KUBESOLO_D2K_NAMESPACE` | `d2k.namespace` |
| `--apiserver-extra-sans` | `KUBESOLO_APISERVER_EXTRA_SANS` | `kubernetes.apiServer.extraSANs` |
| `--startup-timeout` | `KUBESOLO_STARTUP_TIMEOUT` | `kubernetes.apiServer.startupTimeoutSeconds` |
| `--cpu-manager-policy` | `KUBESOLO_CPU_MANAGER_POLICY` | `kubernetes.kubelet.cpuManager.policy` |
| `--cpu-manager-policy-options` | `KUBESOLO_CPU_MANAGER_POLICY_OPTIONS` | `kubernetes.kubelet.cpuManager.policyOptions` |
| `--reserved-cpus` | `KUBESOLO_RESERVED_CPUS` | `kubernetes.kubelet.cpuManager.reservedCPUs` |
| `--system-reserved` | `KUBESOLO_SYSTEM_RESERVED` | `kubernetes.kubelet.systemReserved` |
| `--debug` | `KUBESOLO_DEBUG` | `logging.debug` |
| `--pprof-server` | `KUBESOLO_PPROF_SERVER` | `logging.pprof` |
| `--metrics-bind-address` | `KUBESOLO_METRICS_BIND_ADDRESS` | `metrics.bindAddress` |
| `--metrics-server` | `KUBESOLO_METRICS_SERVER` | `metrics.enabled` |
| `--disable-ipv6` | `KUBESOLO_DISABLE_IPV6` | `network.disableIPv6` |
| `--load-balancer` | `KUBESOLO_LOAD_BALANCER` | `network.loadBalancer.enabled` |
| `--load-balancer-ip` | `KUBESOLO_LOAD_BALANCER_IP` | `network.loadBalancer.ip` |
| `--mtu` | `KUBESOLO_MTU` | `network.mtu` |
| `--node-ip` | `KUBESOLO_NODE_IP` | `network.nodeIP` |
| `--path` | `KUBESOLO_PATH` | `path` |
| `--portainer-edge-async` | `KUBESOLO_PORTAINER_EDGE_ASYNC` | `portainer.async` |
| `--portainer-edge-id` | `KUBESOLO_PORTAINER_EDGE_ID` | `portainer.edgeID` |
| `--portainer-edge-key` | `KUBESOLO_PORTAINER_EDGE_KEY` | `portainer.edgeKey` |
| `--portainer-edge-image` | `KUBESOLO_PORTAINER_EDGE_IMAGE` | `portainer.image` |
| `--container-mode` | `KUBESOLO_CONTAINER_MODE` | `runtime.containerMode` |
| `--container-runtime-endpoint` | `KUBESOLO_CONTAINER_RUNTIME_ENDPOINT` | `runtime.endpoint` |
| `--db-wal-repair` | `KUBESOLO_DB_WAL_REPAIR` | `storage.dbWALRepair` |
| `--local-storage` | `KUBESOLO_LOCAL_STORAGE` | `storage.localPath.enabled` |
| `--local-storage-shared-path` | `KUBESOLO_LOCAL_STORAGE_SHARED_PATH` | `storage.localPath.sharedPath` |

These settings have an environment variable but never had a flag:

| Environment variable | Setting |
|---|---|
| `KUBESOLO_NODE_NAME` | `kubernetes.nodeName` |
| `KUBESOLO_API_ENABLED` | `api.enabled` |
| `KUBESOLO_API_SOCKET_PATH` | `api.socketPath` |
| `KUBESOLO_PPROF_PORT` | `logging.pprofPort` |
| `KUBESOLO_POD_CIDR` | `kubernetes.podCIDR` |
| `KUBESOLO_SERVICE_CIDR` | `kubernetes.serviceCIDR` |
| `KUBESOLO_CLUSTER_DNS` | `kubernetes.clusterDNS` |
| `KUBESOLO_APISERVER_PORT` | `kubernetes.apiServer.port` |
| `KUBESOLO_CONTROLLER_MANAGER_PORT` | `kubernetes.controllerManager.port` |
| `KUBESOLO_KUBELET_PORT` | `kubernetes.kubelet.port` |
| `KUBESOLO_KUBELET_HEALTHZ_PORT` | `kubernetes.kubelet.healthzPort` |
| `KUBESOLO_KUBE_PROXY_HEALTHZ_PORT` | `kubernetes.kubeProxy.healthzPort` |
| `KUBESOLO_WEBHOOK_PORT` | `kubernetes.webhook.port` |
| `KUBESOLO_KINE_PORT` | `storage.kine.port` |
| `KUBESOLO_D2K_PORT` | `d2k.port` |

Four flags have no setting, because they configure nothing:

| Flag | Why |
|---|---|
| `--config` | Names this file, so it cannot live inside it |
| `--print-config` | Prints the resolved document and exits |
| `--version` | Prints the version and exits |
| `--full` | Has had no effect for several releases; retained for compatibility |

---

## See also

- [Configuration API](config-api.md) — managing this file over a socket
- [Install script flags](../installation/flags.md)
- [kubesoloctl](../installation/kubesoloctl.md)
