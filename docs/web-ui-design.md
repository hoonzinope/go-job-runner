# Web UI Design

## Goal

`job-runner`의 Web UI는 운영자가 `Job`과 `Run`을 빠르게 확인하고 조작할 수 있는 관리 화면이다.

핵심 목표는 다음과 같다.

- Job을 생성, 수정, 삭제, 즉시 실행할 수 있어야 한다.
- Run의 상태, 이벤트, 로그, 결과를 확인할 수 있어야 한다.
- API를 직접 호출하지 않아도 주요 운영 작업이 가능해야 한다.

## Scope

MVP 기준으로 아래 기능을 포함한다.

- Job 목록 조회
- Job 상세 조회
- Job 생성 / 수정
- Job 즉시 실행
- Run 목록 조회
- Run 상세 조회
- Run 이벤트 조회
- Run 로그 조회
- Run 결과 조회
- Run 취소

초기 버전에서는 Dashboard 화면을 별도로 두지 않고, `Job 목록`을 홈 화면으로 사용한다.

## Navigation

권장 화면 구조는 다음과 같다.

- `/jobs`
- `/jobs/new`
- `/jobs/:jobId`
- `/jobs/:jobId/edit`
- `/runs`
- `/runs/:runId`

보조 UI는 모달 또는 패널로 처리한다.

- Job 삭제 확인 모달
- Job 즉시 실행 확인 모달
- 이미지 해석 / 검증 패널

## Screens

### 1. Job List

목적:

- 등록된 Job을 한눈에 보고 관리한다.

보여줄 정보:

- Job ID
- Name
- Enabled
- Schedule Type
- Next Run At
- Last Scheduled At
- Concurrency Policy
- Retry Limit
- Timeout Sec
- Updated At

기능:

- 이름 검색
- enabled 필터
- scheduleType 필터
- 페이지 이동
- 상세 이동
- 수정 이동
- 삭제
- 즉시 실행

이 화면이 사실상의 홈 화면 역할을 한다.

### 2. Job Detail

목적:

- 한 Job의 설정과 최근 실행 기록을 함께 본다.

보여줄 정보:

- 기본 설정
  - name
  - description
  - enabled
  - sourceType
  - imageRef
  - imageDigest
  - scheduleType
  - scheduleExpr 또는 intervalSec
  - timezone
  - concurrencyPolicy
  - retryLimit
  - timeoutSec
  - params
  - nextRunAt
  - lastScheduledAt
- 최근 Run 목록

기능:

- 수정
- 삭제
- 즉시 실행
- 최근 Run 상세 이동

### 3. Job Create / Edit

목적:

- Job 설정을 입력하거나 수정한다.

입력 필드:

- name
- description
- enabled
- sourceType
- imageRef
- scheduleType
- scheduleExpr
- intervalSec
- timezone
- concurrencyPolicy
- retryLimit
- timeoutSec
- params

입력 규칙:

- `scheduleType = interval` 인 경우 `intervalSec` 필수
- `scheduleType = cron` 인 경우 `scheduleExpr` 필수
- `params` 는 JSON 형태로 입력하거나 편집 가능해야 한다
- `sourceType` / `scheduleType` / `concurrencyPolicy` 는 선택형으로 제한한다

보조 기능:

- 이미지 후보 확인
- 이미지 resolve 결과 확인
- 입력값 검증 메시지 표시

### 4. Run List

목적:

- 전체 실행 이력을 상태 기준으로 추적한다.

보여줄 정보:

- Run ID
- Job ID
- Job Name
- Scheduled At
- Started At
- Finished At
- Status
- Attempt
- Exit Code
- Updated At

기능:

- jobId 필터
- status 필터
- from / to 필터
- 페이지 이동
- 상세 이동

### 5. Run Detail

목적:

- 한 Run의 상태 변화와 결과를 확인한다.

보여줄 정보:

- Run 기본 정보
- 상태
- 시각 정보
- exit code
- error message
- log path
- result path

하위 섹션:

- Events
- Logs
- Result

기능:

- Run 취소
- 로그 새로고침
- 결과 확인

## Component Notes

### Confirm Modal

다음 작업은 확인 모달을 거친다.

- Job 삭제
- Job 즉시 실행
- Run 취소

### Image Panel

Job 생성 / 수정 시 이미지 관련 지원 UI를 제공한다.

- local / remote 이미지 후보 조회
- image ref resolve
- 유효하지 않은 image ref 에 대한 오류 표시

## API Mapping

Web UI는 아래 API를 사용한다.

- `GET /api/v1/jobs`
- `GET /api/v1/jobs/:jobId`
- `POST /api/v1/jobs`
- `PUT /api/v1/jobs/:jobId`
- `DELETE /api/v1/jobs/:jobId`
- `POST /api/v1/jobs/:jobId/trigger`
- `GET /api/v1/jobs/:jobId/runs`
- `GET /api/v1/runs`
- `GET /api/v1/runs/:runId`
- `POST /api/v1/runs/:runId/cancel`
- `GET /api/v1/runs/:runId/events`
- `GET /api/v1/runs/:runId/logs`
- `GET /api/v1/runs/:runId/result`
- `GET /api/v1/images`
- `GET /api/v1/images/resolve`
- `GET /api/v1/images/:sourceType/candidates`

## MVP Priority

구현 순서는 다음이 적절하다.

1. Job List
2. Job Create / Edit
3. Job Detail
4. Run List
5. Run Detail

## Non-Goals for MVP

초기 버전에서 제외해도 되는 항목:

- 대시보드 카드형 통계 화면
- 실시간 로그 스트리밍
- 다중 선택 일괄 작업
- 사용자 인증 / 권한 제어
- 알림 설정

## Implementation Notes

- UI는 서버 렌더링 기반으로 시작하는 것이 단순하다.
- Job 목록을 홈으로 두면 운영 흐름이 가장 직관적이다.
- Job 상세 안에서 Run 목록을 함께 보여주면 탐색 비용이 줄어든다.
- Run 상세는 Events / Logs / Result 를 분리해도 되고, 초기에는 단일 페이지의 섹션으로 구성해도 된다.
