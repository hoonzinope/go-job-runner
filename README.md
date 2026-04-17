# job-runner

**Docker image 단위의 작업을 스케줄링하고 실행하는 단일 바이너리 스케줄러**
A single-binary job scheduler that executes Docker image-based tasks on a schedule.

---

## Overview

cron 또는 interval 기반 스케줄에 따라 Docker 컨테이너를 실행하고, 실행 이력과 로그를 관리하는 경량 스케줄러입니다.
SQLite와 Docker socket만으로 동작하며, 별도의 외부 의존성이 없습니다.

A lightweight scheduler that runs Docker containers on cron or interval schedules, tracking execution history and logs.
Requires only SQLite and a Docker socket — no external dependencies.

---

## Features

- **Cron / Interval 스케줄** — cron expression 또는 초 단위 interval 지원
- **Concurrency Policy** — `forbid` (중복 실행 방지) / `allow` (독립 실행)
- **Retry / Timeout** — 실패·타임아웃 시 재시도, 실행 시간 제한
  - retry가 발생하면 새로운 Run 레코드가 생성되며, `/api/v1/runs`와 `/api/v1/jobs/:id/runs`에서 그대로 조회됩니다.
- **Run 이력 관리** — 실행 상태, 로그 파일, 이벤트 스트림 조회
- **Run Cancel** — 실행 중인 컨테이너 즉시 중단
- **Web UI** — 작업 등록·수정·즉시 실행, 실행 이력 및 로그 조회
- **REST API** — `/api/v1` prefix, JSON 응답

---

## Requirements

| 항목 | 버전 |
|------|------|
| Go | 1.22+ |
| Docker | Docker daemon 실행 중 (`docker.sock` 접근 가능) |

---

## Build & Run

```bash
# 빌드 / Build
go build -o job-runner ./cmd

# 실행 / Run (설정 파일 지정)
./job-runner --config config.yaml
```

---

## Configuration

`config.yaml` 예시:

```yaml
server:
  host: 0.0.0.0
  port: 8080

store:
  sqlite_path: /data/app.db
  log_root: /data/logs
  artifact_root: /data/artifacts

scheduler:
  due_job_scan_interval_sec: 2
  dispatch_scan_interval_sec: 1
  max_concurrent_runs: 3

image:
  allowed_sources:
    - local
    - remote
  default_source: local
  pull_policy: if_not_present  # always | if_not_present | never
  allowed_prefixes:
    - jobs/
  remote:
    endpoint: http://registry:5000
    insecure: true

ui:
  title: my task scheduler
```

설정 우선순위: `기본값 < config.yaml < 환경 변수`
Priority: `default < config.yaml < environment variables`

---

## Architecture

```
┌─────────────┐     ┌──────────────────────────────────┐
│   Web UI    │     │            Scheduler             │
│  (HTML/JS)  │     │  ┌─────────────┐                │
└──────┬──────┘     │  │ due-job loop│ (next_run_at   │
       │            │  │             │  <= now 스캔)   │
┌──────▼──────┐     │  └──────┬──────┘                │
│  REST API   │     │         │ wakeup                 │
│  /api/v1    │─────►  ┌──────▼──────┐                │
└─────────────┘     │  │dispatch loop│ (pending run    │
                    │  │             │  → worker 할당) │
                    │  └──────┬──────┘                │
                    │         │                        │
                    │  ┌──────▼──────┐                │
                    │  │   worker    │ (image pull     │
                    │  │  goroutine  │  → container    │
                    │  │             │  run → log)     │
                    │  └─────────────┘                │
                    └──────────────────────────────────┘
                                   │
                         ┌─────────▼─────────┐
                         │  SQLite (WAL mode) │
                         │  Docker socket     │
                         └───────────────────┘
```

---

## API Overview

| Method | Path | 설명 |
|--------|------|------|
| `GET` | `/api/v1/jobs` | Job 목록 조회 (페이징) |
| `POST` | `/api/v1/jobs` | Job 생성 |
| `GET` | `/api/v1/jobs/:id` | Job 단건 조회 |
| `PUT` | `/api/v1/jobs/:id` | Job 수정 |
| `DELETE` | `/api/v1/jobs/:id` | Job 삭제 (hard delete) |
| `POST` | `/api/v1/jobs/:id/trigger` | Job 즉시 실행 |
| `GET` | `/api/v1/jobs/:id/runs` | Job 기준 Run 목록 |
| `GET` | `/api/v1/runs` | Run 목록 조회 (페이징) |
| `GET` | `/api/v1/runs/:id` | Run 단건 조회 |
| `POST` | `/api/v1/runs/:id/cancel` | Run 취소 |
| `GET` | `/api/v1/runs/:id/events` | Run 이벤트 조회 |
| `GET` | `/api/v1/runs/:id/logs` | Run 로그 조회 (offset/tail) |
| `GET` | `/api/v1/images` | 이미지 후보 목록 |
| `GET` | `/api/v1/images/resolve` | 이미지 검증/해석 |

---

## Project Structure

```
cmd/
  main.go
internal/
  api/
    handler/          # REST API 핸들러
    ui/               # Web UI 핸들러 + 템플릿
    router.go
  scheduler/
    scheduler.go      # 전체 오케스트레이션
    due_job.go        # due-job loop
    dispatch.go       # dispatch loop
    worker.go         # worker goroutine
  store/
    db.go             # SQLite 초기화 (WAL 모드)
    job_repo.go
    run_repo.go
    event_repo.go
  model/              # Job / Run / RunEvent 모델
  image/              # local / remote 이미지 소스
  executor/
    docker.go         # 컨테이너 실행
  config/             # 설정 로드 및 검증
  log/
    writer.go         # stdout/stderr 파일 저장
```

---

## License

MIT
