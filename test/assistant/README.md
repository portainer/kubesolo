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
