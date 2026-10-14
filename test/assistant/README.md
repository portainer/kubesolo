# Assistant triage test set

Incidents with the diagnosis the assistant should reach, to compare models and
to check changes to the [knowledge file](../../build/assistant/knowledge.md)
and the triage prompt before they ship.

| Directory | Cases |
|---|---|
| `cases/dev` | Tune the knowledge and the prompt against these |
| `cases/heldout` | Do not tune against these: they show whether a change generalises |

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
| **2B** (default) | 10 | 11 | 61 s | 2.2 GB |
| 4B | 9 | 10 | 148 s | 3.6 GB |

The 0.8B suggests `restart_component` for nearly everything. The 4B does no
better than the 2B, takes 2.4 times as long, and leaves no room on a 4 GB host.

Before the schema listed the action after the summary and the cause, the 2B
scored 9 and 8: the model chose the action first and reasoned after it. Twelve
held-out cases are few — one case is 8 points — so a change to the knowledge
should hold or raise the held-out score, not just the dev one.
