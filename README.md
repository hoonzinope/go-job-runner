<div align="center">

<img src="assets/logo.png" alt="job-runner logo" width="220" />

# job-runner

**A lightweight Docker workload scheduler with a built-in Web UI.**

Schedule Docker image workloads on cron or interval schedules. Tracks run history, logs, and artifacts entirely on disk — no external dependencies beyond SQLite.

[![Docker Image](https://img.shields.io/docker/v/hoonzinope/image-job-runner?label=docker&logo=docker)](https://hub.docker.com/r/hoonzinope/image-job-runner)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.21+-00ADD8?logo=go)](go.mod)

[한국어](README.ko.md)

</div>

---

## Table of Contents

- [Features](#features)
- [Architecture](#architecture)
- [Current Limitations / Non-goals](#current-limitations--non-goals)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [API Reference](#api-reference)
- [Web UI](#web-ui)
- [Sample Images](#sample-images)
- [Development](#development)
- [Project Structure](#project-structure)
- [License](#license)

---

## Features

- **Cron & interval schedules** — standard cron expressions or fixed-second intervals with timezone support
- **Concurrency policy** — `allow` (run in parallel) or `forbid` (skip if already running; stale `running` rows past the effective timeout are auto-closed so the schedule can recover)
- **Retry & timeout** — configurable per-job retry limit and execution timeout
- **Persistent run history** — every run, event, log, and artifact is stored locally via SQLite
- **Web UI** — browse jobs and runs, view logs, trigger/cancel runs from the browser
- **REST API** — full CRUD and operational endpoints under `/api/v1`
- **Dual image sources** — pull images from a local path or a remote registry
- **Executor policy** — Docker execution is constrained to documented network and resource settings; privileged mode and extra volume mounts are not exposed
- **Self-contained** — single binary, single config file, no external broker or database server

---

## Architecture

```
┌──────────────────────────────────────────┐
│                  job-runner              │
│                                          │
│  ┌─────────────┐   ┌──────────────────┐  │
│  │  REST API   │   │     Web UI       │  │
│  │  /api/v1    │   │  /jobs  /runs    │  │
│  └──────┬──────┘   └────────┬─────────┘  │
│         │                   │            │
│  ┌──────▼───────────────────▼─────────┐  │
│  │            Service Layer           │  │
│  └──────────────────┬─────────────────┘  │
│                     │                    │
│  ┌──────────────────▼─────────────────┐  │
│  │             Scheduler              │  │
│  │  due-job loop → dispatch loop      │  │
│  │            → worker goroutines     │  │
│  └──────────────────┬─────────────────┘  │
│                     │                    │
│  ┌──────────────────▼──────────────────┐ │
│  │   SQLite Store  │  Docker Executor  │ │
│  │  jobs/runs/     │  image pull       │ │
│  │  events/logs    │  container run    │ │
│  └─────────────────┴───────────────────┘ │
└──────────────────────────────────────────┘
```

**Scheduler internals:**

| Loop | Responsibility |
|---|---|
| Due-job loop | Scans `next_run_at`, inserts `pending` runs |
| Dispatch loop | Picks up `pending` runs, hands them to workers |
| Worker goroutine | Resolves image → pulls → runs container → writes logs → updates status |

---

## Current Limitations / Non-goals

job-runner is intentionally scoped as a lightweight, self-contained Docker workload scheduler. It does not currently guarantee production-grade orchestration behavior.

- The runner assumes a single-node deployment. Distributed workers, multi-node coordination, Kubernetes, and Docker Swarm are out of scope.
- Scheduling is best-effort. Jobs are checked and dispatched by periodic scheduler loops, so strict real-time execution is not guaranteed.
- Built-in authentication and authorization are not provided for the Web UI or REST API.
- External exposure should be protected by reverse proxy authentication, a VPN, or an IP allowlist.
- Docker socket access is required. Because that socket can affect the host Docker daemon, run the service only in trusted environments.
- Retention pruning exists for completed run history, logs, and artifacts, but long-term storage management policy should still be planned by the operator.
- High availability, failover, and cross-node recovery are not current goals.

---

## Quick Start

### 1. Pull the image

```bash
docker pull hoonzinope/image-job-runner:latest
# or pin to a release
docker pull hoonzinope/image-job-runner:v1.0.1
```

### 2. Create a config file

```bash
mkdir -p ~/docker_v/image-job-runner
cp config.example.yaml ~/docker_v/image-job-runner/config.yml
# edit the file as needed
```

### 3. Run

```bash
docker run -d --name image-job-runner \
  -p 8888:8888 \
  -v ~/docker_v/image-job-runner/config.yml:/app/config.yml:ro \
  -v ~/docker_v/image-job-runner:/app/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  hoonzinope/image-job-runner:latest
```

### 4. Open the UI

```
http://localhost:8888/jobs
```

> Security note: the built-in Web UI and REST API do not include authentication or authorization. If you bind the server to `0.0.0.0` or any non-loopback address, place it behind a reverse proxy with auth, a VPN, or an IP allowlist before exposing it outside a trusted network.

---

## Configuration

The repository ships `config.example.yaml` as a template. Keep your active config outside the repository.

```yaml
server:
  # Use 127.0.0.1 for local-only access.
  # Binding to 0.0.0.0 exposes the UI/API on the network.
  host: 0.0.0.0
  port: 8888

store:
  sqlite_path: ./data/app.db
  log_root: ./data/logs
  log_path_pattern: job-%d/run-%d/run.log
  artifact_root: ./data/artifacts
  result_path_pattern: job-%d/run-%d/result.json

scheduler:
  due_job_scan_interval_sec: 2   # how often to check for due jobs
  dispatch_scan_interval_sec: 1  # how often to dispatch pending runs
  max_concurrent_runs: 2         # global worker pool size
  default_timeout_sec: 3600      # applied when timeoutSec is omitted
  max_timeout_sec: 86400         # largest accepted per-job timeout
  allow_unlimited_timeout: false # allow timeoutSec=0 only when explicitly true

image:
  allowed_sources:               # which source types are permitted
    - local
    - remote
  default_source: local
  pull_policy: if_not_present    # always | if_not_present | never
  allowed_prefixes:              # image ref must match one of these
    - example-image/
    - jobs/
  remote:
    endpoint: http://192.168.215.1:5001
    insecure: true

executor:
  network_mode: bridge           # bridge | none; host is intentionally rejected
  read_only_rootfs: false        # enable only after checking workloads
  memory_limit_mb: 0             # 0 = unlimited
  cpu_limit: 0                   # 0 = unlimited
  cleanup_containers: true       # remove runner-created containers after each run
  stop_grace_period_sec: 10      # grace period before Docker force kill on stop
  orphan_recovery_on_startup: true # remove stale runner-created containers on startup

retention:
  enabled: true
  prune_interval_sec: 3600       # how often to run cleanup
  run_history_days: 30           # delete completed runs and their events after this age
  success_log_days: 7            # delete successful run logs after this age
  failed_log_days: 30            # delete failed/timeout/cancelled logs after this age
  artifact_days: 14              # delete result/artifact files after this age
  max_log_bytes_per_run: 10485760 # truncate completed run logs above 10 MiB
  max_total_storage_bytes: 10737418240 # delete oldest completed-run files above 10 GiB
```

### Key fields

| Field | Description |
|---|---|
| `store.sqlite_path` | Path to the SQLite database file |
| `store.log_root` | Root directory for run log files |
| `store.artifact_root` | Root directory for run result/artifact files |
| `scheduler.max_concurrent_runs` | Maximum number of runs executing simultaneously |
| `scheduler.default_timeout_sec` | Timeout used when a job omits `timeoutSec`; default config is 3600 seconds |
| `scheduler.max_timeout_sec` | Maximum accepted per-job timeout; requests above this are rejected |
| `scheduler.allow_unlimited_timeout` | Allows `timeoutSec=0` only when `true`; disabled by default because unlimited jobs can starve workers |
| `image.pull_policy` | `always` re-pulls on every run; `if_not_present` skips if the image exists locally |
| `image.allowed_prefixes` | Whitelist of image ref prefixes; requests outside this list are rejected |
| `executor.network_mode` | Docker network mode for job containers; only `bridge` and `none` are accepted |
| `executor.read_only_rootfs` | When `true`, job containers run with a read-only root filesystem |
| `executor.memory_limit_mb` | Optional memory cap for job containers (`0` disables the limit) |
| `executor.cpu_limit` | Optional CPU cap for job containers (`0` disables the limit) |
| `executor.cleanup_containers` | Removes runner-created containers after success, failure, timeout, or cancel |
| `executor.stop_grace_period_sec` | Docker stop grace period used before force-killing containers during timeout/cancel/recovery |
| `executor.orphan_recovery_on_startup` | Scans Docker for runner-managed containers and removes them before scheduling starts |
| `retention.enabled` | Enables scheduled pruning on startup and every `prune_interval_sec` seconds |
| `retention.run_history_days` | Deletes completed run rows older than this many days; run events are deleted by SQLite cascade |
| `retention.success_log_days` | Deletes log files for successful completed runs older than this many days |
| `retention.failed_log_days` | Deletes log files for failed, timed out, and cancelled completed runs older than this many days |
| `retention.artifact_days` | Deletes result/artifact files for completed runs older than this many days |
| `retention.max_log_bytes_per_run` | Truncates oversized log files for completed runs; `0` disables the limit |
| `retention.max_total_storage_bytes` | Deletes oldest completed-run log/artifact files until managed storage is below the cap; `0` disables the cap |

Built-in authentication is not provided. If the service is reachable on a non-loopback address, protect it with a reverse proxy, VPN, or IP allowlist before using it outside a trusted environment.

The Docker executor uses human-readable container names with a unique suffix: `job-runner-run-<jobID>-<runID>-<uuid>`. Every runner-created container is labeled with `go-job-runner.managed=true`, `go-job-runner=true`, `go-job-runner.job-id=<jobID>`, and `go-job-runner.run-id=<runID>`. The executor logs the concrete container name when a run starts, launches, exits, or is stopped so you can correlate Docker activity with the run record. Startup recovery still removes any runner-managed orphan containers by label.

When `executor.cleanup_containers=true`, containers are removed after success and failure. Timeout, API cancel, and process shutdown cancellation first send `docker stop -t <stop_grace_period_sec>` and then attempt container removal. If cleanup fails after the container process exits, the run fails with the Docker cleanup error so the leak is visible in run state and logs. If cleanup fails during timeout/cancel handling, timeout/cancel remains the run outcome and the labeled container can be found by startup recovery.

When `executor.orphan_recovery_on_startup=true`, scheduler startup scans Docker for `go-job-runner.managed=true` containers and removes them before dispatching pending work. This reconciles Docker resources left by runner crashes or failed cleanup. SQLite run status is not rewritten during this scan; run-state reconciliation remains status-driven through normal scheduler recovery, while Docker cleanup is label-driven and idempotent. The unique container suffix prevents a second launch from colliding with a stale name while still leaving a readable prefix in logs.

The Docker executor uses the host Docker socket, so the runner can affect the local daemon. The project does not expose privileged mode or arbitrary extra volume mounts through config. Network mode and resource limits are the supported executor-level controls; anything else should be treated as out of scope for this release.

Retention pruning only targets completed runs (`success`, `failed`, `timeout`, `cancelled`). It does not delete `pending`, `running`, or `cancelling` run rows, events, logs, or artifacts. File pruning is constrained to paths under `store.log_root` and `store.artifact_root`, so paths outside managed storage are ignored.

---

## API Reference

All endpoints are under `/api/v1`.

### Jobs

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/jobs` | List jobs (paginated) |
| `POST` | `/api/v1/jobs` | Create a job |
| `GET` | `/api/v1/jobs/:id` | Get a job |
| `PUT` | `/api/v1/jobs/:id` | Update a job |
| `DELETE` | `/api/v1/jobs/:id` | Delete a job |
| `POST` | `/api/v1/jobs/:id/trigger` | Trigger a job immediately |
| `GET` | `/api/v1/jobs/:id/runs` | List runs for a job |

**Job request body fields:**

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique job name |
| `enabled` | bool | Whether the job is active |
| `sourceType` | `local` \| `remote` | Image source |
| `imageRef` | string | Image reference (e.g. `jobs/my-image:latest`) |
| `scheduleType` | `cron` \| `interval` | Schedule type |
| `scheduleExpr` | string | Cron expression (when `scheduleType=cron`) |
| `intervalSec` | number | Interval in seconds (when `scheduleType=interval`) |
| `timezone` | string | IANA timezone (default: `UTC`) |
| `concurrencyPolicy` | `allow` \| `forbid` | What to do when the job is already running |
| `retryLimit` | number | Number of retries on failure (0 = no retry) |
| `timeoutSec` | number | Execution timeout in seconds; omitted uses `scheduler.default_timeout_sec`; `0` is rejected unless `scheduler.allow_unlimited_timeout=true`; values above `scheduler.max_timeout_sec` are rejected |
| `params` | object | Arbitrary JSON passed to the container as environment variables |

### Runs

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/runs` | List runs (paginated) |
| `GET` | `/api/v1/runs/:id` | Get a run |
| `POST` | `/api/v1/runs/:id/cancel` | Cancel a run |
| `GET` | `/api/v1/runs/:id/events` | Get run events |
| `GET` | `/api/v1/runs/:id/logs` | Stream or page run logs |

**Run status values:** `pending` → `running` → `success` | `failed` | `timeout` | `cancelled`

**Log query parameters:**

| Parameter | Description |
|---|---|
| `offset` | Byte offset to start reading from |
| `limit` | Maximum number of bytes to return |
| `tail` | Return last N lines |

### Images

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/images` | List image candidates |
| `GET` | `/api/v1/images/resolve` | Resolve or validate an image ref |

---

## Web UI

The built-in Web UI is served at `/jobs`.

| Path | Description |
|---|---|
| `/jobs` | Job list — search, filter, create, trigger, delete |
| `/jobs/new` | Create a new job |
| `/jobs/:id` | Job detail — settings and recent run history |
| `/jobs/:id/edit` | Edit a job |
| `/runs` | Run list — filter by status, date range, job |
| `/runs/:id` | Run detail — status, events timeline, logs, result |

---

## Sample Images

Two sample workloads are included under `example-image/`:

| Path | Source type | Notes |
|---|---|---|
| `example-image/local` | `local` | Builds and runs directly from the local filesystem |
| `example-image/remote` | `remote` | Pushed to a local registry; requires the `remote` endpoint in config |

Both print a `hello world image test` message with execution metadata, are scheduled once per minute, and start after a short delay — useful for verifying scheduling and log capture.

---

## Development

**Prerequisites:** Go 1.21+, Docker

```bash
# Build
go build -o bin/job-runner ./cmd

# Run all tests
go test ./...

# Run locally
cp config.example.yaml config.yml
./bin/job-runner --config config.yml
```

**Docker-based development:**

```bash
docker build -t job-runner:local .

docker run --rm -p 8888:8888 \
  -v $(pwd)/config.yml:/app/config.yml:ro \
  -v $(pwd)/data:/app/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  job-runner:local
```

**Build the sample images:**

```bash
# local sample
docker build -t example-image/local:latest example-image/local/

# remote sample (push to your local registry)
docker build -t localhost:5001/example-image/remote:latest example-image/remote/
docker push localhost:5001/example-image/remote:latest
```

---

## Project Structure

```text
cmd/
  main.go                    # entry point

internal/
  api/
    router.go                # route registration
    handler/                 # REST API handlers (jobs, runs, images)
    ui/                      # Web UI handlers and HTML templates

  scheduler/
    scheduler.go             # orchestration and lifecycle
    due_job.go               # due-job scanning loop
    dispatch.go              # dispatch loop
    worker.go                # worker goroutine (pull → run → log → status)

  store/
    db.go                    # SQLite init and migrations
    job_repo.go
    run_repo.go
    event_repo.go

  model/                     # Job, Run, RunEvent types

  image/
    local.go                 # local filesystem image source
    remote.go                # remote registry image source
    source.go                # source interface

  executor/
    docker.go                # Docker container execution

  service/
    job_service.go
    run_service.go

  config/                    # config loading and validation
  log/                       # run log and result writers/readers
```

---

## License

MIT — see [LICENSE](LICENSE) for details.
