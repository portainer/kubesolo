# KubeSolo performance suite

Three scripts, each boots the container image with the e2e harness
(`../e2e/lib.sh`), measures, emits JSON plus a GitHub job-summary table, tears
down, and fails when a gate is breached. On failure each one saves a 30s CPU
profile, a heap profile and a goroutine dump of the still-running node to
`profiles/` before teardown.

| Script | Question it answers | Local |
|--------|---------------------|-------|
| `bench.sh` | Does an idle node boot fast and stay small and quiet? | `make bench` |
| `load.sh` | Does the control plane hold up under write, watch and pod churn, and go back to idle afterwards? | `make load` |
| `scale.sh` | Does a node with a large datastore history still idle, LIST and restart in time, and is that history compacted so the database stops growing? | `make scale` |

All take `IMAGE` (`PERF_IMAGE` for make) and `KUBESOLOCTL`. `load.sh` and
`scale.sh` drive load with `loadgen/` (client-go, no extra binaries); they build
it on the fly unless `LOADGEN` points at a binary.

## Where they run

| Trigger | What |
|---------|------|
| Every PR image (`ci-publish.yml`) | `bench.sh` ceilings (no density probe) + a 30s `load.sh`, amd64 |
| Nightly (`nightly.yml`) | `bench.sh`, `load.sh`, `scale.sh` on amd64 + arm64 |
| Weekly (`weekly.yml`) | 5h `../e2e/soak.sh`; `load.sh` inside a 512 MiB / 1 CPU container |
| Release (`release.yml`) | `bench.sh` + `load.sh` against the develop image before the release is created |

## Gates

Gates are **absolute ceilings** where healthy and broken are far apart, so
shared-runner variance cannot flake them and no baseline is needed. Idle CPU is
the clearest case: issue #254 (kine's poll loop spinning) idled at ~1.4 cores,
a healthy node at a few hundredths of a core.

| Script | Gate | Default |
|--------|------|---------|
| bench | `IDLE_CPU_MAX` idle cores | 0.25 |
| bench | `IDLE_RSS_MAX` idle MiB | 768 |
| load | idle cores before and after load (`IDLE_CPU_MAX`) | 0.25 |
| load | churn errors / watch write errors / missed watch events | 0 |
| load | `CHURN_P99_MAX_MS` | 1000 |
| load | `WATCH_LAG_P99_MAX_MS` | 2000 |
| load | `POD_STARTUP_P99_MAX_S` | 60 |
| load | `RECOVERY_RSS_GROWTH` MiB across the run | 256 |
| load | `RECOVERY_GOROUTINE_GROWTH` across the run | 200 |
| scale | idle cores with the large database, and after restarting on it | 0.25 |
| scale | `LIST_P99_MAX_MS` (every object) | 2000 |
| scale | `RESTART_READY_MAX_S` | 120 |
| scale | `COMPACT_TIMEOUT`: seconds after each fill for live data to drop under `COMPACTED_LIVE_MAX_MIB` (64) | 900 |
| scale | `DB_GROWTH_MAX_PCT`: database and WAL file growth over a second, identical fill | 25 |

`bench.sh` additionally gates boot times, idle RSS and image size on *relative*
regression against a committed `baseline.<arch>.json` (`TOLERANCE`, default
+20%). Without a baseline that part only reports.

Constrained hardware: set `KS_MEMORY` / `KS_CPUS` (e.g. `512m` / `1`) and
`../e2e/boot.sh` applies them to the container and restarts it, so the boot and
everything after it run under the limits. `load.sh` and `../e2e/soak.sh` use
`boot.sh`; `bench.sh` boots itself to time the install.

## Metrics

`result.<arch>.json` (bench):

| Field | Meaning |
|-------|---------|
| `boot_to_api_s` | container start → API server reachable (kubeconfig merged) |
| `boot_to_node_ready_s` | → node `Ready` |
| `boot_to_first_pod_s` | → a trivial pod `Running` (time-to-useful-cluster) |
| `idle_rss_mib` | whole-container memory at idle (`docker stats`) |
| `idle_cpu_cores` | whole-container CPU at idle, from the cgroup's `cpu.stat` |
| `idle_api_rps` | API requests per second at idle |
| `image_size_mib` | distribution cost |
| `max_pods` | most pods scheduled before a scale step failed to go Ready |

`load.<arch>.json`: `idle_before` / `idle_after` (cores, MiB, goroutines) and
one object per scenario — `churn` (ops/s, p50/p99/max ms, errors), `watch`
(watchers, writes, missed events, delivery lag p50/p99), `pods` (pod creation
→ Ready p50/p99) — each with the node's `cpu_cores` during the scenario.

`scale.<arch>.json`: fill rate, database size after fill and after restart,
LIST p50/p99, idle CPU and RSS with the large database, restart → node Ready,
and `compaction`: live data (`apiserver_storage_size_bytes`, the database less
its free pages) after the fill and once compacted, how long each compaction
took, and the database and WAL file sizes after each fill.

The API server compacts every 5 minutes, to the revision it saw at the previous
tick, so a fill is compacted 5 to 10 minutes after it lands. SQLite never
shrinks its files while running, so the check is that the second fill reuses
the pages the first fill's compacted revisions freed. Without compaction the
database file roughly doubles. The WAL keeps its high-water mark too: kine's
post-compaction checkpoint is `FULL`, not `TRUNCATE`.

## Establishing a bench baseline

1. Let a green nightly run produce `perf-<arch>` artifacts (or run `make bench`).
2. Inspect the numbers; when they look representative, commit them:
   ```bash
   cp result.amd64.json baseline.amd64.json
   cp result.arm64.json baseline.arm64.json
   git add test/perf/baseline.*.json
   ```

Re-baseline deliberately (e.g. after a Kubernetes bump), never to make a red run
green. Baselines are per runner type: one taken on a laptop or a test VM does not
transfer to GitHub-hosted runners.
