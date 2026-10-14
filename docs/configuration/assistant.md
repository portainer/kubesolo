# Assistant

KubeSolo can run its own small language model to answer questions about the node. The model is served by [llama.cpp](https://github.com/ggml-org/llama.cpp)'s `llama-server`, which KubeSolo starts as a child process — not as a pod — so the assistant still works when containerd, the database or the API server is broken.

It is **off by default**. When on, nothing runs until a question is asked: KubeSolo starts `llama-server`, waits for the model to load, asks, and stops it again, so the model holds no memory between questions.

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
| `assistant.contextSize` | `KUBESOLO_ASSISTANT_CONTEXT_SIZE` | `8192` |

Changes take effect when KubeSolo restarts. `contextSize` is the context window in tokens: raising it lets longer questions through and raises the memory held while answering.

---

## The model

KubeSolo only ever reads the model. `assistant.model` takes one of three forms:

| Value | Model |
|---|---|
| `""` (default) | Qwen3.5-2B at 4-bit (about 1.3 GB), fetched from `docker.io/portainerci/kubesolo-assistant-model`, pinned by digest |
| `/var/lib/kubesolo-models/mine.gguf` | A GGUF file on the host |
| `registry.example.com/models/mine:v1` | A model artifact in a registry |

A model from a registry is fetched once containerd is up, in the background; a question asked before it arrives is answered with "the model is still being downloaded". If the registry cannot be reached, KubeSolo retries every minute. Once fetched, the model is kept in containerd's store, in a namespace of its own that the kubelet's image cleanup does not touch, and is used from there on every later start without contacting the registry.

```bash
journalctl -u kubesolo | grep "model is ready"
```

Registry mirrors and credentials come from the same [`hosts.toml` files](registry.md) as the rest of the node. A model artifact cannot be fetched with `runtime.endpoint` set, since it goes through KubeSolo's own containerd; use a file path there.

### Publishing a model artifact

The model is run where containerd stores it, so the artifact's single layer must be the GGUF file itself. Push it with [ORAS](https://oras.land):

```bash
oras push registry.example.com/models/mine:v1 mine.gguf:application/vnd.docker.ai.gguf.v3
```

An ordinary container image will not do — a `COPY mine.gguf` in a Dockerfile puts the model inside a tar archive, which would have to be unpacked into a second copy. KubeSolo refuses such an image, and a multi-platform one, and says why.

## Asking a question

```bash
sudo kubesoloctl assistant ask "why would containerd keep restarting?"
```

Expect a wait: on two CPUs the model loads in seconds, then reads the question at roughly 50 tokens a second and writes the answer at roughly 10. Only one question is answered at a time; a second one while the first is in progress is refused.

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
| `409` | Another question is being answered |
| `500` | The model could not be read or loaded; the error says why |

---

## Troubleshooting

```bash
journalctl -u kubesolo | grep assistant      # start, stop, and why it was disabled
```

With `logging.debug: true`, `llama-server`'s own output appears in the log too. The extracted binary is at `/var/lib/kubesolo/assistant/llama-server`.
