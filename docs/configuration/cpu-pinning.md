# CPU Pinning

By default every pod shares all of the host's CPUs. The Linux scheduler moves pods between cores freely and enforces limits with CFS quota, which is fine for most workloads but leaves latency at the mercy of whatever else is running. A workload that must not be preempted — audio processing, motion control, machine vision, protocol gateways — sees this as jitter.

KubeSolo can hand such a workload **exclusive CPU cores** instead. Set `--cpu-manager-policy=static` and pods that qualify get cores no other pod is allowed to run on.

> This is not a substitute for a real-time kernel. Exclusive cores stop *other pods* from interfering. They do nothing about kernel threads, interrupt handlers, or KubeSolo itself. See [Beyond exclusive cores](#beyond-exclusive-cores).

---

## Enabling CPU pinning

| Flag | Env var | Default |
|---|---|---|
| `--cpu-manager-policy` | `KUBESOLO_CPU_MANAGER_POLICY` | `none` |
| `--cpu-manager-policy-options` | `KUBESOLO_CPU_MANAGER_POLICY_OPTIONS` | _(empty)_ |
| `--reserved-cpus` | `KUBESOLO_RESERVED_CPUS` | `0` when the static policy is used |

```bash
curl -sfL https://get.kubesolo.io | sudo sh -s -- --cpu-manager-policy=static --reserved-cpus=0
```

`--reserved-cpus` is the cpuset held back for the host and KubeSolo itself. Those CPUs are never handed out as exclusive cores. The static policy refuses to start without a reservation, so KubeSolo defaults it to CPU `0` and logs that it has done so. Naming the CPU explicitly is deliberate: it is more predictable than letting the kubelet pick one.

Everything left over is the **shared pool**, used by every pod that does not qualify for exclusive cores. At least one CPU must remain, so the static policy cannot be enabled on a single-CPU host.

CPU pinning is **not supported in container mode**, where the container's own cpuset bounds what KubeSolo can actually pin to. KubeSolo refuses to start rather than pretend otherwise.

---

## Making a workload eligible

Exclusive cores are only given to containers that ask for whole CPUs under the **Guaranteed** QoS class. That means, for **every** container in the pod:

- `requests` and `limits` are both set, for both `cpu` and `memory`
- they are equal
- the CPU value is a whole number

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: audio-processor
spec:
  containers:
    - name: audio-processor
      image: example/audio-processor:1.0
      resources:
        requests:
          cpu: "2"
          memory: "512Mi"
        limits:
          cpu: "2"
          memory: "512Mi"
```

This pod gets two cores to itself.

### Getting it wrong fails quietly

If the pod does not qualify, **it still runs**. It simply shares the CPU pool like any other pod. There is no error and no warning, so a workload can look healthy while getting none of the isolation it was deployed for.

The mistakes that cause it:

| Written | Result |
|---|---|
| `cpu: "1500m"` | Not a whole number, so no exclusive cores |
| `limits` set, `requests` omitted | Burstable QoS, so no exclusive cores |
| `memory` limit omitted | Burstable QoS, so no exclusive cores |
| One sidecar in the pod breaking any of the above | The **whole pod** drops to Burstable |

Always verify rather than assume.

---

## Verifying it worked

There is no `kubectl` command that reports which cores a pod holds. Two ways to check:

**From inside the container** — the allowed CPU list should be the exclusive cores, not the whole machine:

```bash
kubectl exec audio-processor -- grep Cpus_allowed_list /proc/self/status
```

```
Cpus_allowed_list:	2-3
```

A pod that is *not* pinned shows the shared pool instead (for example `1-7` on an 8-CPU host reserving CPU 0).

**From the node** — the kubelet's podresources API is the authoritative source, and lists the CPU IDs assigned to every container:

```
/var/lib/kubesolo/kubelet/pod-resources/kubelet.sock
```

It speaks gRPC (`v1.PodResourcesLister`), so it needs a client rather than `curl`.

Also worth checking that the reservation took effect — `kubectl describe node` should show allocatable CPU reduced by the reserved count.

---

## Policy options

`--cpu-manager-policy-options` takes a comma-separated list of `key=value` pairs and only applies to the static policy.

| Option | What it does |
|---|---|
| `full-pcpus-only` | Allocate whole physical cores, never individual hyperthread siblings. Stops a sibling thread on the same core from stealing cache and pipeline capacity. |
| `strict-cpu-reservation` | Keep pods entirely off the reserved CPUs. Without this, shared-pool pods may still run on them. |
| `distribute-cpus-across-numa` | Spread an allocation evenly across NUMA nodes when it needs more than one. |
| `prefer-align-cpus-by-uncorecache` | Prefer cores sharing an uncore (last-level) cache. Best effort — pods are still admitted if it cannot be satisfied. |

For a latency-sensitive workload on typical hardware:

```bash
--cpu-manager-policy=static \
--cpu-manager-policy-options=full-pcpus-only=true,strict-cpu-reservation=true \
--reserved-cpus=0
```

`prefer-align-cpus-by-uncorecache` and `distribute-cpus-across-numa` cannot both be enabled. KubeSolo rejects the combination at startup.

The two alpha options in upstream Kubernetes, `align-by-socket` and `distribute-cpus-across-cores`, are not exposed. They need an extra feature gate and neither helps on the single-socket hardware KubeSolo targets.

---

## Sizing and admission

KubeSolo has no scheduler — the NodeSetter webhook assigns every pod to the one node directly. This is usually what you want on a single node, but it changes what happens when a pod cannot fit.

On a full cluster the scheduler would place the pod elsewhere. Here, the pod is already bound before the kubelet checks whether enough exclusive CPUs are free. If they are not, the pod fails with `UnexpectedAdmissionError` and **is not retried**. It stays dead until you delete and recreate it.

So size deliberately:

```
exclusive cores available = total CPUs - reserved CPUs
```

On a 4-CPU host reserving CPU 0, three cores can be handed out. A fourth requested exclusive core kills the pod. Leave headroom, and remember the shared pool still has to run CoreDNS, any storage provisioner, and the Portainer agent if it is enabled.

---

## Changing the configuration

The kubelet records the active policy and CPU pool in a checkpoint file at `/var/lib/kubesolo/kubelet/cpu_manager_state`. If that file disagrees with the configuration, the kubelet refuses to start. Upstream's advice is to drain the node and delete the file — which a single node cannot do.

KubeSolo handles this itself. When the CPU manager settings change, it removes the checkpoint before starting the kubelet, so a configuration change is just a restart.

One consequence worth knowing: **workloads holding exclusive cores return to the shared pool** when the checkpoint is discarded, and stay there until they are restarted. They keep running, and there is no error. After changing CPU manager settings, restart your pinned workloads:

```bash
kubectl rollout restart deployment/audio-processor
```

---

## Beyond exclusive cores

Exclusive cores solve the noisy-neighbour half of the problem. For genuinely deterministic latency, the host also has to stop competing for those cores. None of this is something KubeSolo can do for you:

**Keep the kernel off the pinned cores.** Add `isolcpus` and `nohz_full` to the kernel command line for the cores you intend to hand out, so the scheduler and the timer tick leave them alone:

```
isolcpus=2,3 nohz_full=2,3 rcu_nocbs=2,3
```

**Move interrupts away.** Pin IRQ affinity to the reserved CPUs, otherwise device interrupts will land on the audio cores anyway. Check `/proc/interrupts` and set `/proc/irq/*/smp_affinity_list`.

**Confine KubeSolo itself.** `--reserved-cpus` tells the kubelet what to hold back for the system, but does not constrain the KubeSolo process. Add a `CPUAffinity` to the service unit:

```ini
[Service]
CPUAffinity=0
```

**Consider a real-time kernel.** For hard deadlines rather than merely low average latency, `PREEMPT_RT` is the next step.

Without these, exclusive cores reduce jitter but do not eliminate it.

---

## Reference

- [Kubernetes CPU management policies](https://kubernetes.io/docs/tasks/administer-cluster/cpu-management-policies/)
- [Kubernetes QoS classes](https://kubernetes.io/docs/concepts/workloads/pods/pod-qos/)
- [Container mode](container-mode.md) — CPU pinning is unsupported there
