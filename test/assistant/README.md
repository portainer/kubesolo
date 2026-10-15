# Assistant triage test set

Incidents with the diagnosis the assistant should reach, to compare models and
to check changes to the [knowledge file](../../build/assistant/knowledge.md)
and the triage prompt before they ship.

| Directory | Cases |
|---|---|
| `cases/dev` | Tune the knowledge and the prompt against these |
| `cases/heldout` | Do not tune against these: they show whether a change generalises |
| `cases/generated` | Made by breaking a test node on purpose; see [Generating cases](#generating-cases) |

Each case is an incident as KubeSolo records it, and what is expected of the
diagnosis:

```json
{
  "incident": {"trigger": "...", "time": "...", "snapshot": {...}},
  "expect": {
    "actions": ["restart_component"],
    "mentions": ["kubelet"]
  }
}
```

`actions` lists every acceptable action; `mentions` lists words of which the
summary or cause should contain one. The `real-*` cases were recorded on a
test node; the others carry error strings quoted in KubeSolo's GitHub issues,
over the routine startup lines a healthy node logs.

## Running it

The evaluation runs the real triage code, `Diagnose`, against a real
`llama-server` and model, so it needs a host that can run the model: amd64 or
arm64, 2 CPUs, 4 GB. Stop KubeSolo first so the model has the memory.

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -tags assistant_eval -o assistant-eval.test ./pkg/assistant
scp -r assistant-eval.test test/assistant/cases build/assistant/knowledge.md node:eval/

# on the node
ASSISTANT_EVAL_LLAMA_SERVER=/var/lib/kubesolo/assistant/llama-server \
ASSISTANT_EVAL_MODEL=/var/lib/models/Qwen3.5-2B-Q4_K_M.gguf \
ASSISTANT_EVAL_KNOWLEDGE=eval/knowledge.md \
ASSISTANT_EVAL_CASES=eval/cases \
  eval/assistant-eval.test -test.run TestEvalTriage -test.v -test.timeout 0
```

It prints a line per case and a score per directory:

```
dev: actions 10/12, named 12/12, summaries cut 0, 63s a case
```

## Results

Qwen3.5 at Q4_K_M, knowledge v1, on 2 vCPUs (amd64, 3.9 GiB), October 2026.
Actions right out of 12:

| Model | Dev | Held out | Time a case | Peak memory |
|---|---|---|---|---|
| 0.8B (before the order fix below) | 6 | 4 | 30 s | — |
| **2B** (default) | 11 | 9 | 59 s | 2.2 GB |
| 4B | 9 | 10 | 148 s | 3.6 GB |

The 0.8B suggests `restart_component` for nearly everything. The 4B does no
better than the 2B, takes 2.4 times as long, and leaves no room on a 4 GB host.

Before the schema listed the action after the summary and the cause, the 2B
scored 9 and 8: the model chose the action first and reasoned after it. Twelve
held-out cases are few — one case is 8 points — so a change to the knowledge
should hold or raise the held-out score, not just the dev one.

### When to act

Each diagnosis carries `actionProbability`, the probability the model gave
its action among the six, read from its token probabilities. The model's own
rating of itself, which the schema asked for until then, said `low` for 23 of
the 24 cases, right or wrong; the probability tells them apart. With the 2B, a non-`report_only` action that is
wrong is a false action:

| Act only at or above | Right actions kept (dev / held out) | False actions |
|---|---|---|
| — | 7 / 6 | 1 / 1 |
| 0.5 | 7 / 6 | 1 / 0 |
| 0.8 | 5 / 3 | 0 / 0 |
| 0.9 | 4 / 2 | 0 / 0 |

KubeSolo offers an action only at 0.8. The false actions below it were
`restart_component` for orphaned containers (0.75) and for a datastore slowed
by a full disk (0.48).

Small changes to the prompt move borderline cases: dropping the model's rating
of itself took the 2B from 10 / 11 to 11 / 9, all of the change below 0.8.
Judge a change by what passes 0.8 as much as by the totals.

## Generating cases

[`inject/generate.sh`](inject/generate.sh) breaks a KubeSolo node on purpose,
waits for the assistant to record and triage the incident, saves it as a case,
and puts the node right. The label comes from the fault, which is known, not
from the model; the model's own triage is kept beside it, under `source`, for
reference.

It runs on the node, as root, and **breaks it**: use a test node, with the
assistant and the API enabled, and `kubectl` installed.

```bash
# on the node: six faults, three runs each, about 4 hours
./generate.sh /root/generated 3 kubelet-down kine-blocked runtime-unreachable killed coredns-firewall db-corrupt
```

| Fault | How | Labelled |
|---|---|---|
| `kubelet-down` | Rejects the kubelet's health port for 7 minutes | `restart_component` |
| `kine-blocked` | Rejects kine's port for 7 minutes | `restart_component` |
| `runtime-unreachable` | Moves containerd's socket away for 7 minutes | `restart_component` |
| `killed` | `kill -9` | `report_only` |
| `disk-full` | Fills the disk to 32 MiB short of full for 7 minutes, then deletes the pods evicted meanwhile | `free_disk_space` |
| `coredns-firewall` | Rejects pod traffic to the API server and restarts CoreDNS | `report_only` |
| `db-corrupt` | Overwrites part of the datastore, lets KubeSolo fail on it, then puts the saved copy back | `repair_database` |

Odd runs add noise workloads, a crash-looping pod and an unpullable image, so
each fault is also seen among unrelated warnings. Each fault starts from a
node that has been healthy for 90 seconds, so that it is an outage of its own,
and a fault that produces no incident within 25 minutes is logged and skipped.

A full disk once recorded no incident, as every component stayed healthy;
the watcher now checks the host's disk and memory too. While the disk is full,
the kubelet evicts pods and their replacements by the thousand: on the test
node that left 7,303 failed pods, held in the API server's memory (KubeSolo
grew to 1.8 GB), until they were deleted.

The first `disk-full` case was triaged `prune_images`, rated high (0.85),
against its label, `free_disk_space`: the snapshot says how full the disk is,
not what fills it, so the model cannot tell the two apart.

### First generated batch

Six faults, three runs each, on the 2 vCPU test node, October 2026. The 2B
triaged them live, as KubeSolo does:

| Fault | Right | Offered at 0.8 |
|---|---|---|
| `db-corrupt` | 3 / 3 | 3, all right |
| `coredns-firewall` | 3 / 3 | — (`report_only`) |
| `kubelet-down` | 2 / 3 | 2, both right |
| `runtime-unreachable` | 2 / 3 | 1, right |
| `killed` | 2 / 3 | — |
| `kine-blocked` | 1 / 3 | 0: wrong actions at 0.72 and 0.41 |

13 of 18 right; every wrong action rated below 0.8, so none would have been
offered. A blocked kine is the weak spot: the model reaches for
`repair_database` or `free_disk_space`.
