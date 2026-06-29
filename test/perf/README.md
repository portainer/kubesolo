# KubeSolo performance baseline

`bench.sh` boots the container image and records the metrics that matter for a
constrained edge distro, then gates against a committed baseline on *relative*
regression (runner-variance tolerant). It reuses the e2e harness (`../e2e/lib.sh`)
for boot + teardown.

Runs nightly per arch via [`.github/workflows/nightly.yaml`](../../.github/workflows/nightly.yaml);
locally via `make bench PERF_IMAGE=portainer/kubesolo:dev`.

## Metrics (`result.<arch>.json`)

| Field | Meaning |
|-------|---------|
| `boot_to_api_s` | container start → API server reachable (kubeconfig merged) |
| `boot_to_node_ready_s` | → node `Ready` |
| `boot_to_first_pod_s` | → a trivial pod `Running` (time-to-useful-cluster) |
| `idle_rss_mib` | whole-container memory at idle (`docker stats`) |
| `image_size_mib` | distribution cost |
| `max_pods` | most pods scheduled before a scale step failed to go Ready |

## Tunables

`OUT`, `BASELINE`, `TOLERANCE` (default `0.20` = +20%), `DENSITY_MAX` (default
`50`, `0` disables), `DENSITY_STEP` (default `10`), `SETTLE` (default `25`s).

## Establishing a baseline

No baseline is committed initially, so the gate is **report-only** until you
capture one from a trusted run:

1. Let a green nightly run produce `perf-<arch>` artifacts (or run `make bench`).
2. Inspect the numbers; when they look representative, commit them:
   ```bash
   cp result.amd64.json baseline.amd64.json
   cp result.arm64.json baseline.arm64.json
   git add test/perf/baseline.*.json
   ```
3. Subsequent runs fail if a metric regresses beyond `TOLERANCE` vs the baseline.

Re-baseline deliberately (e.g. after a Kubernetes bump) — don't let it drift up
silently. Gate on *relative* change, never absolute numbers, so shared-runner
variance doesn't cause flakes.

## Why no kube-burner / clusterloader2

The density probe is a deliberately lightweight `kubectl scale` loop — no extra
binary, no heavyweight load generator, matching the constrained targets KubeSolo
runs on. Swap in `kube-burner` later if you need pod-startup-latency percentiles.
