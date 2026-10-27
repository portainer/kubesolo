# Assistant

KubeSolo can run its own small language model to answer questions about the node. The model is served by [llama.cpp](https://github.com/ggml-org/llama.cpp)'s `llama-server`, which KubeSolo starts as a child process — not as a pod — so the assistant still works when containerd, the database or the API server is broken.

It is **off by default**. When on, the model runs only to answer a question or to [triage an incident](#incidents): KubeSolo starts `llama-server`, waits for the model to load, asks, and stops it again, so the model holds no memory in between.

---

## Requirements

| | |
|---|---|
| Architecture | `amd64` or `arm64` |
| CPUs | 2 or more |
| Memory | 4 GB or more (checked as at least 3.5 GiB, since a 4 GB host reports a little less) |
| Configuration API | `api.enabled: true`: the assistant is asked through it |

On a host that falls short, KubeSolo logs a warning at startup and runs without the assistant.

While answering, `llama-server` uses about the size of the model file plus its context: around 2 GB for a 2B-parameter model at 4-bit with the default context.

---

## Enabling the assistant

```yaml
# /etc/kubesolo/config.yaml
api:
  enabled: true
assistant:
  enabled: true
```

| Setting | Env var | Default |
|---|---|---|
| `assistant.enabled` | `KUBESOLO_ASSISTANT_ENABLED` | `false` |
| `assistant.model` | `KUBESOLO_ASSISTANT_MODEL` | `""` |
| `assistant.knowledge` | `KUBESOLO_ASSISTANT_KNOWLEDGE` | `""` |
| `assistant.contextSize` | `KUBESOLO_ASSISTANT_CONTEXT_SIZE` | `8192` |
| `assistant.autoRestart` | `KUBESOLO_ASSISTANT_AUTO_RESTART` | `false` |

Changes take effect when KubeSolo restarts. `contextSize` is the context window in tokens: raising it lets longer questions through and raises the memory held while answering.

---

## The model

KubeSolo only ever reads the model. `assistant.model` takes one of three forms:

| Value | Model |
|---|---|
| `""` (default) | Qwen3.5-2B at 4-bit (about 1.3 GB), fetched from `docker.io/portainerci/kubesolo-assistant-model`, pinned by digest |
| `/var/lib/kubesolo-models/mine.gguf` | A GGUF file on the host |
| `registry.example.com/models/mine:v1` | A model artifact in a registry |
| `pvc:ai/models/mine.gguf` | A GGUF file in a PersistentVolumeClaim: `pvc:<namespace>/<claim>/<path in the volume>` |

A model from a registry is fetched once containerd is up, in the background; a question asked before it arrives is answered with "the model is still being downloaded". If the registry cannot be reached, KubeSolo retries every minute. Once fetched, the model is kept in containerd's store, in a namespace of its own that the kubelet's image cleanup does not touch, and is used from there on every later start without contacting the registry.

```bash
journalctl -u kubesolo | grep "model is ready"
```

Registry mirrors and credentials come from the same [`hosts.toml` files](registry.md) as the rest of the node. A model artifact cannot be fetched with `runtime.endpoint` set, since it goes through KubeSolo's own containerd; use a file path there.

A model in a PersistentVolumeClaim lets a Kubernetes Job download or build it. KubeSolo reads it in place, from the volume's directory on the host, once the claim is bound and the file is there, and looks again every minute until then. The volume must be a directory on the host, as the local-path provisioner's are; a path that leads out of the volume, through `..` or a link, is refused.

### Without a registry

An air-gapped node takes the model and the knowledge as files. On a machine that can reach the registry:

```bash
oras pull docker.io/portainerci/kubesolo-assistant-model:qwen3.5-2b-q4_k_m -o model/
oras pull docker.io/portainerci/kubesolo-assistant-knowledge:0.1.0 -o knowledge/
```

Copy `model/model.gguf` and `knowledge/knowledge.md` to the node, and name them:

```yaml
assistant:
  enabled: true
  model: /var/lib/kubesolo-models/model.gguf
  knowledge: /var/lib/kubesolo-models/knowledge.md
```

### Publishing a model artifact

The model is run where containerd stores it, so the artifact's single layer must be the GGUF file itself. Push it with [ORAS](https://oras.land):

```bash
oras push registry.example.com/models/mine:v1 mine.gguf:application/vnd.docker.ai.gguf.v3
```

An ordinary container image will not do — a `COPY mine.gguf` in a Dockerfile puts the model inside a tar archive, which would have to be unpacked into a second copy. KubeSolo refuses such an image, and a multi-platform one, and says why.

## The knowledge

To triage an incident, the model is given a **knowledge file**: a Markdown description of KubeSolo's known failures, their signs in the logs and events, and the action that fixes each. Improving the triage means improving this file, not KubeSolo, so it is published apart from KubeSolo releases. `assistant.knowledge` takes the same three forms as the model:

| Value | Knowledge |
|---|---|
| `""` (default) | [`build/assistant/knowledge.md`](../../build/assistant/knowledge.md), fetched from `docker.io/portainerci/kubesolo-assistant-knowledge` |
| `/etc/kubesolo/knowledge.md` | A Markdown file on the host |
| `registry.example.com/kubesolo/knowledge:v2` | A knowledge artifact in a registry |
| `pvc:ai/models/knowledge.md` | A Markdown file in a PersistentVolumeClaim |

Its `## Harmless lines` section lists, in quotes, text that healthy nodes log routinely — startup noise a small model would otherwise take for the cause. KubeSolo removes any log line or event containing one of those texts before the model reads an incident, so quote only exact text that never shows a fault. The incident itself keeps every line.

It is fetched and kept the same way as the model. The file can be at most 12 KB — its share of the model's prompt, alongside the incident — and KubeSolo refuses a larger one. Publish one with ORAS, the file itself as the single layer:

```bash
oras push registry.example.com/kubesolo/knowledge:v2 knowledge.md:text/markdown
```

## Asking a question

```bash
sudo kubesoloctl assistant ask "why would containerd keep restarting?"
```

Expect a wait: on two CPUs the model loads in seconds, then reads the question at roughly 50 tokens a second and writes the answer at roughly 10. Only one question is answered at a time; a question while another is answered, or an incident triaged, is refused.

The same request over the configuration API:

```bash
sudo curl -s --unix-socket /var/lib/kubesolo/config.sock \
  -X POST http://localhost/api/v1/assistant:ask \
  -H 'Content-Type: application/json' \
  -d '{"question": "why would containerd keep restarting?"}'
```

```json
{"answer": "..."}
```

| Status | Meaning |
|---|---|
| `200` | The answer |
| `400` | The question is missing or empty |
| `404` | The assistant is not enabled |
| `409` | Another question is being answered, or an incident triaged |
| `500` | The model could not be read or loaded; the error says why |

---

## Incidents

With the assistant enabled, KubeSolo records an **incident** when something goes wrong with KubeSolo itself:

| Trigger | Recorded |
|---|---|
| KubeSolo did not shut down cleanly: a crash, a fatal error, a kill or a power loss | On the next start. A crash within 15 minutes of the previous one is part of the same crash loop and is not recorded again |
| A control plane component (containerd, kine, API server, controller manager, kubelet, webhook, CoreDNS) fails its health check for 5 minutes, or the host does: its disk 90% or more used, or under 200 MiB of memory available | Once per outage, however many components it takes down. A new outage needs every component, and the host, healthy in between |

Each incident carries a snapshot of the node at the time: each component's health, KubeSolo's latest error log lines, recent Kubernetes warning events, and disk and memory use. A snapshot is a few KB, and at most 12 KB. The 100 most recent incidents are kept, under `/var/lib/kubesolo/assistant/incidents/`.

### Triage

Every incident is triaged as soon as it is recorded: the model reads the [knowledge](#the-knowledge) and the snapshot, and answers with a diagnosis. Incidents are triaged one at a time, oldest first, and an incident is served only once it has been — so it never appears without its diagnosis.

```json
"triage": {
  "status": "triaged",
  "time": "2026-10-14T03:12:40Z",
  "summary": "The kubelet stopped answering its health check for 5 minutes.",
  "cause": "The kubelet is stuck or has stopped.",
  "action": "restart_component",
  "actionProbability": 0.97,
  "rating": "high"
}
```

| `status` | Meaning |
|---|---|
| `triaged` | The model diagnosed it |
| `failed` | The model ran but gave no usable diagnosis; `reason` says why |
| `skipped` | The model was not run; `reason` says why: less than 2.5 GiB of memory was available, or the model or knowledge could not be used, or was still not fetched after an hour |

`action` is one of `restart_component`, `repair_database`, `prune_images`, `clean_orphaned_containers`, `free_disk_space` and `report_only`. It is only a suggestion: KubeSolo takes no action. `actionProbability` is the probability the model gave that action among the six, read from its own token probabilities rather than asked of it, and `rating` puts it in words: `high` from 0.8, `medium` from 0.5, `low` below that or when unknown. On two CPUs a triage takes a few minutes, mostly reading the prompt.

An incident from a crash is triaged once KubeSolo has started. In a crash loop, KubeSolo does not stay up long enough for that, so the incident is triaged on the first run that does.

```bash
sudo kubesoloctl assistant incidents          # list, oldest first
sudo kubesoloctl assistant incidents <id>     # one incident, as JSON
```

They are served by the configuration API, including on the Portainer agent's socket:

| Request | Returns |
|---|---|
| `GET /api/v1/assistant/incidents?since=<id>` | Each triaged incident after `<id>` (all without it), oldest first: `id`, `time`, `kind`, `trigger`, `triage`, `review` |
| `GET /api/v1/assistant/incidents/<id>` | The incident with its snapshot, triage and review, or `404` |
| `POST /api/v1/assistant/incidents/<id>:approve` | Records an approval; see [Reviews](#reviews) |
| `POST /api/v1/assistant/incidents/<id>:reject` | Records a rejection |
| `GET /api/v1/assistant/report` | The node's health and latest incidents; see [The report](#the-report) |

IDs sort in time order, and incidents are served in that order, so a poller asks for everything after the newest ID it has seen, and nothing needs acknowledging.

### Reviews

A person can say whether a diagnosis was right. Reviews are kept with the incident, as the labelled data the model and its knowledge are measured — and later trained — against.

**Approving an action carries it out.** First its preconditions are checked again against the node as it is now, not as it was at the incident; if they no longer hold, the action is refused and nothing is done. The incident's `actionRun` says how it went: `running`, `succeeded`, `failed` or `refused`, with the reason. One action is carried out at a time; approving while another runs keeps the approval and answers `409` — approve again shortly. An action that failed for the same components in the last 6 hours is not carried out again, approved or automatic: it is refused, naming that failure, since it would not help until something else changes. Nor is a restart carried out when a failing component listens on its port yet refuses the probe: something between them, such as a firewall rule, blocks the port, and a restart would leave it blocked, so a person must clear it.

| Action | Carried out as | Only when |
|---|---|---|
| `restart_component` | KubeSolo is restarted by the upgrade executor, from outside it, and checked healthy, as `POST /api/v1/restart` does: it reads the configuration file again, and if KubeSolo or the Portainer agent does not come back, the configuration it last ran healthy on is put back. Pods keep running across it, as across an upgrade. It has succeeded only once every component passes its health check again; one still failing 5 minutes later fails it | A component still fails its health check |
| `repair_database` | As a restart, with the datastore's WAL files dropped while KubeSolo is stopped, as `storage.dbWALRepair` does at start. The datastore as it was is kept under `/var/lib/kubesolo/upgrade/repair-<id>/` | SQLite's integrity check fails now: a sound datastore is never repaired, as repairing drops its latest writes |
| `prune_images` | Images that no container uses, running or stopped, are removed; pinned ones, such as the pause image, are kept. **An image imported on an air-gapped node cannot be pulled again** | The disk is 90% or more used, and some image is unused |
| `clean_orphaned_containers` | The processes of each container whose cgroup outlives it — the runtime reports it exited, or no longer knows it, while the kubelet may have started a second copy — are killed. Its pod is left alone | Such containers are found in two scans 10 seconds apart |
| `free_disk_space`, `report_only` | Not carried out: a person must act | — |

With `assistant.autoRestart: true`, a `restart_component` rated `high` is carried out without approval, when a component still fails, at most once an hour; its `actionRun` says `"auto": true`. Every other action waits for approval.

A datastore damaged badly enough usually stops KubeSolo from starting, so the assistant cannot run to repair it; `storage.dbWALRepair: true` repairs it at start.

Actions are not carried out in container mode, where KubeSolo cannot restart itself.

```bash
sudo kubesoloctl assistant approve <id> --note "kubelet was wedged"
sudo kubesoloctl assistant reject <id> --action repair_database --note "lease UID mismatch"
```

Over the API, both take an optional body, `{"action": "...", "note": "..."}`, and return the incident:

```json
"review": {
  "verdict": "rejected",
  "action": "repair_database",
  "note": "lease UID mismatch",
  "time": "2026-10-15T09:00:00Z"
}
```

| | Approve | Reject |
|---|---|---|
| Allowed when | The action is rated `high` and is not `report_only` | Any served incident, even one whose triage failed or was skipped |
| `action` | The suggested one | The right one, if the reviewer knows it; a missed fix behind `report_only` is worth recording too |
| Refused with | `409` when no action is offered, `400` for another action | `400` for an unknown action |

A later review replaces an earlier one. Reviewed incidents outlast the 100 kept: they are kept up to 1,000, and only the oldest unreviewed incidents are pruned before then.

Reviewed incidents that name the right action can be written out as test cases, in the format of KubeSolo's [triage test set](../../test/assistant/README.md), to measure the model and its knowledge against real failures:

```bash
sudo kubesoloctl assistant export ./cases
```

### The report

The report is the node's health now and its 10 latest incidents, each with where its action stands:

```bash
sudo kubesoloctl assistant report          # for a person
sudo kubesoloctl assistant report --json   # for a program
```

| `state` | Meaning |
|---|---|
| `awaiting approval` | The action is rated `high` and nobody has reviewed it yet |
| `approved` / `rejected` | Reviewed |
| `action running` / `succeeded` / `failed` / `refused` | The approved action, carried out |
| `not offered` | The action is rated below `high`, so it is only reported; or it failed for the same components in the last 6 hours, or it is a restart that would leave a blocked port blocked; `note` says which |
| `report only` | The model found nothing to do, or something a person must do |
| `triage failed` / `triage skipped` | No diagnosis; the triage's `reason` says why |

Over the API, `GET /api/v1/assistant/report` returns it as JSON, and `?format=text` as plain text, with the same content as the command prints. `totalIncidents` and `awaitingApproval` count every incident kept, not only the 10 shown.

---

## Troubleshooting

```bash
journalctl -u kubesolo | grep assistant      # start, stop, and why it was disabled
```

With `logging.debug: true`, `llama-server`'s own output appears in the log too. The extracted binary is at `/var/lib/kubesolo/assistant/llama-server`.
