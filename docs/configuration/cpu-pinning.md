# CPU Pinning

By default every pod shares all of the host's CPUs. The scheduler moves them between cores freely, which is fine for most workloads but leaves latency at the mercy of whatever else is running. Workloads that must not be preempted — audio processing, motion control, machine vision, protocol gateways — see this as jitter.

Set `--cpu-manager-policy=static` and pods that qualify get **exclusive cores** no other pod may run on:

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- \
  --cpu-manager-policy=static \
  --cpu-manager-policy-options=full-pcpus-only=true,strict-cpu-reservation=true \
  --reserved-cpus=0
```

> Exclusive cores stop _other pods_ interfering. They do nothing about kernel threads, interrupts, or KubeSolo itself. Deterministic latency also needs [host tuning](#host-tuning), which KubeSolo cannot do for you.

---

## Flags

| Flag                           | Env var                               | Default                            |
| ------------------------------ | ------------------------------------- | ---------------------------------- |
| `--cpu-manager-policy`         | `KUBESOLO_CPU_MANAGER_POLICY`         | `none`                             |
| `--cpu-manager-policy-options` | `KUBESOLO_CPU_MANAGER_POLICY_OPTIONS` | _(empty)_                          |
| `--reserved-cpus`              | `KUBESOLO_RESERVED_CPUS`              | `0` when the static policy is used |

`--reserved-cpus` is held back for the host and KubeSolo, and never handed out as an exclusive core. It is a cpuset of CPU *indexes*, not a count — `0` reserves one CPU (number 0) and `0-1` reserves two, so on a 4-CPU host they leave 3 and 2 allocatable respectively. The static policy refuses to start without a reservation, so KubeSolo defaults it to CPU `0` and logs that it did. Naming the CPU is more predictable than letting the kubelet pick one.

Everything left over is the **shared pool**. At least one CPU must remain, so this needs a host with 2+ CPUs.

Unsupported in [container mode](container-mode.md) — the container's own cpuset bounds what can actually be pinned, so KubeSolo refuses to start rather than pretend otherwise.

---

## Making a workload eligible

Two requirements, for **every** container in the pod:

1. **Guaranteed Quality of Service (QoS)** — `cpu` and `memory` both constrained, requests equal to limits.
2. **A whole-number CPU value.**

```yaml
resources:
  requests: { cpu: "2", memory: "512Mi" }
  limits: { cpu: "2", memory: "512Mi" }
```

Guaranteed QoS is what makes exclusive cores safe: the pod is capped at exactly what it asked for, so it can never want more than the cores it owns. Burstable pods request less than their ceiling precisely so they can burst, which is incompatible with owning a fixed set of cores — they stay in the shared pool.

Kubernetes also disables CFS quota throttling for containers holding exclusive CPUs, so a pinned workload is not stalled at the end of each quota period. On by default since Kubernetes 1.33; no configuration needed.

### Getting it wrong fails quietly

A pod that does not qualify **still runs** — it just shares the pool. No error, no warning, so it can look healthy while getting none of the isolation it was deployed for.

| Written                                              | QoS        | Exclusive cores?                 |
| ---------------------------------------------------- | ---------- | -------------------------------- |
| `requests` = `limits` = `cpu: "2"`, `memory` set     | Guaranteed | Yes                              |
| `limits` only: `cpu: "1"`, `memory` set              | Guaranteed | Yes — requests default to limits |
| `requests` = `limits` = `cpu: "1500m"`, `memory` set | Guaranteed | **No** — not a whole number      |
| `cpu: "1"`, no `memory`                              | Burstable  | **No**                           |
| One sidecar breaking any of the above                | Burstable  | **No** — for the whole pod       |

Row three is the trap: a fractional value stays Guaranteed and looks correct, but half a CPU cannot be a dedicated core. Verify rather than assume.

---

## Verifying

No `kubectl` command reports which cores a pod holds. From inside the container:

```bash
kubectl exec audio-processor -- grep Cpus_allowed_list /proc/self/status
```

```
Cpus_allowed_list:	1-2
```

Two exclusive cores on a 4-CPU host reserving CPU 0. An unpinned pod shows the shared pool instead.

The kubelet's podresources API at `/var/lib/kubesolo/kubelet/pod-resources/kubelet.sock` is authoritative and lists CPU IDs per container. It speaks gRPC (`v1.PodResourcesLister`), so it needs a client rather than `curl`.

`kubectl describe node` should also show allocatable CPU reduced by the reserved count.

---

## Policy options

Comma-separated `key=value`, static policy only.

| Option                             | What it does                                                                                                                                                |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `full-pcpus-only`                  | Allocate whole physical cores, never single hyperthread siblings, so a sibling thread cannot steal cache and pipeline capacity. No-op on hosts without SMT. |
| `strict-cpu-reservation`           | Keep pods entirely off the reserved CPUs. Without it, shared-pool pods may still run there.                                                                 |
| `distribute-cpus-across-numa`      | Spread an allocation evenly across NUMA nodes when it needs more than one.                                                                                  |
| `prefer-align-cpus-by-uncorecache` | Prefer cores sharing a last-level cache, reducing cache latency and cross-cache contention. Best effort — pods are still admitted if it cannot be met.      |

`prefer-align-cpus-by-uncorecache` and `distribute-cpus-across-numa` are mutually exclusive; KubeSolo rejects the pair at startup. The upstream alpha options `align-by-socket` and `distribute-cpus-across-cores` are not exposed — they need an extra feature gate and neither helps on single-socket edge hardware.

---

## Sizing

```
exclusive cores available = total CPUs - reserved CPUs
```

KubeSolo has no scheduler; the NodeSetter webhook binds every pod to the one node directly. A pod that does not fit is therefore **not rescheduled** — it is bound first, then rejected by the kubelet, and stays dead until recreated:

```
Pod was rejected: Node didn't have enough resource: cpu, requested: 1000, used: 1100, capacity: 2000
```

On a 4-CPU host reserving CPU 0, three cores can be handed out. Leave headroom — the shared pool still runs CoreDNS, any storage provisioner, and the Portainer agent.

---

## Changing the configuration

The kubelet records the active policy and CPU pool in `/var/lib/kubesolo/kubelet/cpu_manager_state` and refuses to start if that file disagrees with its config. Upstream's fix is to drain the node and delete it, which a single node cannot do — so KubeSolo removes it for you. A config change is just a restart.

- **Embedded containerd** (default): restarting KubeSolo tears down containerd too, so pods are recreated and re-pinned automatically. Cores may differ from the ones held before — allocation is by count, not identity.
- **External runtime** (`--container-runtime-endpoint`): containers survive the restart, so a pinned workload drops back to the shared pool — still running, no error — until you `kubectl rollout restart` it.

Widening the reservation also shrinks allocatable CPU, and anything that no longer fits is rejected permanently as above. Check what is running first.

---

## Host tuning

Exclusive cores solve noisy neighbours. Deterministic latency also needs the host to stop competing for those cores.

**Isolate them from the kernel.** On the kernel command line, for the cores you intend to hand out:

```
isolcpus=1-3 nohz_full=1-3 rcu_nocbs=1-3
```

**Steer interrupts away**, via `/proc/irq/*/smp_affinity_list` — check `/proc/interrupts` for what is firing. Then stop `irqbalance` undoing it: it rebalances periodically and overwrites manual affinities. Either disable the service, or ban the isolated cores with `IRQBALANCE_BANNED_CPUS` in `/etc/sysconfig/irqbalance`.

**Confine KubeSolo itself.** `--reserved-cpus` tells the kubelet what to hold back; it does not constrain the KubeSolo process:

```ini
[Service]
CPUAffinity=0
```

**Consider `PREEMPT_RT`** for hard deadlines rather than merely low average latency.

Without these, exclusive cores reduce jitter but do not eliminate it.

> **Coming from OpenShift?** Its Node Tuning Operator applied all of the above from a single `PerformanceProfile` — kernel args, TuneD, kubelet managers and irqbalance together. KubeSolo gives you direct control instead, which means these steps are yours to own.

---

## Reference

- [Kubernetes CPU management policies](https://kubernetes.io/docs/tasks/administer-cluster/cpu-management-policies/)
- [Kubernetes QoS classes](https://kubernetes.io/docs/concepts/workloads/pods/pod-qos/)
- [Intel: CPU pinning and isolation in Kubernetes](https://www.intel.com/content/www/us/en/developer/articles/technical/cpu-management-cpu-pinning-isolation-kubernetes.html)
- [Testing CPU pinning](cpu-pinning-testing.md) — hands-on walkthrough to verify it on a real host
- [Container mode](container-mode.md) — CPU pinning is unsupported there
