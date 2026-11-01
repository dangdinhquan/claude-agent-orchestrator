# Technical Design Document (TDD)

Project: Claude Agent Orchestrator
Repository: dangdinhquan/claude-agent-orchestrator

## 1. Mục tiêu

Xây dựng một Go service làm orchestration layer cho nhiều tiến trình Claude Agent chạy song song, mỗi tiến trình gắn với một profile/login riêng biệt. Service này cung cấp API cho opencode hoặc client khác, chịu trách nhiệm:

- quản lý một pool các Claude Agent worker
- tách profile/login bằng environment riêng
- sticky session theo session_id
- load balancing khi có nhiều worker sẵn sàng
- failover và auto-recovery khi worker chết
- health check, metrics, logging và audit

## 2. Vấn đề cần giải quyết

Khi chạy nhiều Claude Agent trên cùng máy hoặc cùng môi trường mà không tách biệt đầy đủ, sẽ có các rủi ro sau:

- profile chồng chéo nếu dùng cùng HOME/config directory
- session bị trộn lẫn giữa các user/login khác nhau
- shared secrets hoặc env variable gây xung đột auth
- không có sticky routing cho từng session
- không có failover khi process chết
- khó quản lý phân phối request theo tài nguyên có sẵn

## 3. Scope

### 3.1 Trong scope

- multi-worker manager cho Claude Agent
- profile isolation theo directory/env riêng
- session lifecycle management
- sticky routing
- load balancing
- failover và retry
- API server in Go
- observability

### 3.2 Ngoài scope

- sửa logic bên trong Claude CLI/SDK
- xây dựng giao diện web UI cho người dùng
- thay đổi model logic của Claude itself
- quản lý các instance trên nhiều node/cluster chưa ở MVP

## 4. Non-goals

- không coi mỗi login là “thủ tục đăng nhập mà API gateway lưu token” theo kiểu centralized auth
- không share config giữa các profile
- không hỗ trợ session migration cross-node ở v1 nếu cluster chưa có distributed state

## 5. Kiến trúc tổng quan

Hệ thống gồm 5 layer chính:

1. API Gateway (Go)
2. Orchestrator / Scheduler
3. Worker Manager
4. Claude Agent Worker
5. Storage & Observability

```text
+------------------------+
|  OpenCode / Client     |
+-----------+------------+
            |
            v
+------------------------+
|  Go API Gateway        |
|  - auth                |
|  - routing             |
|  - session mgmt        |
|  - streaming           |
+-----------+------------+
            |
            v
+------------------------+
|  Scheduler / Router    |
|  - sticky session       |
|  - LB                  |
|  - failover            |
+-----------+------------+
            |
            v
+------------------------+
|  Worker Manager        |
|  - start/stop/restart  |
|  - health checks       |
+-----------+------------+
            |
            v
+------------------------+
|  Claude Worker Pool    |
|  - isolated profile    |
|  - one process each    |
|  - local Claude CLI    |
+------------------------+
```

## 6. Mô hình dữ liệu

### 6.1 Profile

```go
type Profile struct {
    ID            string    `json:"id"`
    TenantID      string    `json:"tenant_id"`
    Name          string    `json:"name"`
    LoginID       string    `json:"login_id"`
    Status        string    `json:"status"`
    ClaudePath    string    `json:"claude_path"`
    HomeDir       string    `json:"home_dir"`
    ConfigDir     string    `json:"config_dir"`
    WorkDir       string    `json:"work_dir"`
    LogDir        string    `json:"log_dir"`
    SecretRef     string    `json:"secret_ref,omitempty"`
    CreatedAt     time.Time `json:"created_at"`
    UpdatedAt     time.Time `json:"updated_at"`
    Env           map[string]string `json:"env,omitempty"`
}
```

### 6.2 Worker

```go
type Worker struct {
    ID          string    `json:"id"`
    ProfileID   string    `json:"profile_id"`
    Hostname    string    `json:"hostname"`
    PID         int       `json:"pid"`
    Status      string    `json:"status"`
    ActiveSessions int     `json:"active_sessions"`
    QueueDepth  int       `json:"queue_depth"`
    CPUUsage    float64   `json:"cpu_usage"`
    MemoryUsage uint64    `json:"memory_usage"`
    LastHeartbeat time.Time `json:"last_heartbeat"`
    CreatedAt   time.Time `json:"created_at"`
}
```

### 6.3 Session

```go
type Session struct {
    ID          string     `json:"id"`
    TenantID    string     `json:"tenant_id"`
    ProfileID   string     `json:"profile_id"`
    WorkerID    string     `json:"worker_id,omitempty"`
    Status      string     `json:"status"`
    CreatedAt   time.Time  `json:"created_at"`
    UpdatedAt   time.Time  `json:"updated_at"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}
```

## 7. Profile isolation design

Mỗi profile phải có môi trường chạy riêng hoàn toàn.

### 7.1 Environment per profile

- HOME = /var/lib/claude/{profile-id}/home
- XDG_CONFIG_HOME = /var/lib/claude/{profile-id}/config
- XDG_CACHE_HOME = /var/lib/claude/{profile-id}/cache
- WORKDIR = /var/lib/claude/{profile-id}/workspace
- LOGDIR = /var/log/claude/{profile-id}

Dùng env riêng cho từng worker:

```go
func (p Profile) Env() []string {
    return []string{
        "HOME=" + p.HomeDir,
        "XDG_CONFIG_HOME=" + p.ConfigDir,
        "XDG_CACHE_HOME=" + p.HomeDir + "/cache",
        "CLAUDE_CONFIG_DIR=" + p.ConfigDir,
        "CLAUDE_WORKSPACE_DIR=" + p.WorkDir,
    }
}
```

### 7.2 Security isolation

- secret được mount từ Secret Manager hoặc environment provider
- không lưu credential raw trong DB
- mỗi profile có permission scope riêng
- work_dir không được access bởi profile khác

## 8. Worker lifecycle

### 8.1 Start flow

1. API/CLI receives create profile request
2. Manager validates profile config
3. Manager creates local filesystem directories
4. Manager resolves secret
5. Manager creates `exec.Cmd` for Claude process
6. Manager sets isolated env and working directory
7. Manager starts process and records PID/health info
8. Manager registers worker in state store

### 8.2 Stop flow

- graceful shutdown with SIGTERM
- wait timeout 10s
- then SIGKILL if still alive
- update state to stopped and release session mapping

### 8.3 Restart flow

- worker manager does periodic health checks
- if worker unhealthy beyond threshold, it restarts with same profile config
- session mapping is re-evaluated after restart

## 9. Sticky session và load balancing

### 9.1 Sticky routing policy

- session_id được dùng làm key routing
- nếu session đã được bind tới worker A, request tiếp theo ưu tiên route tới A
- nếu A đang healthy, không route sang worker khác

### 9.2 Load balancing policy

Khi session chưa có mapping, chọn worker dựa trên:

1. worker healthy
2. worker not over capacity
3. least active sessions
4. least queue depth
5. best health score
6. weighted round robin

Pseudo-code:

```go
func SelectWorker(sessions SessionStore, workers []Worker, sessionID string) (*Worker, error) {
    if w, ok := sessions.GetStickyWorker(sessionID); ok && workerHealthy(w) {
        return w, nil
    }

    candidates := filterHealthy(workers)
    candidates = filterCapacity(candidates)
    if len(candidates) == 0 {
        return nil, ErrNoHealthyWorker
    }

    sort.Slice(candidates, func(i, j int) bool {
        if candidates[i].QueueDepth != candidates[j].QueueDepth {
            return candidates[i].QueueDepth < candidates[j].QueueDepth
        }
        return candidates[i].ActiveSessions < candidates[j].ActiveSessions
    })

    return &candidates[0], nil
}
```

## 10. API design

### 10.1 REST endpoints

#### Profile API

- POST /v1/profiles
- GET /v1/profiles
- GET /v1/profiles/{id}
- DELETE /v1/profiles/{id}

#### Session API

- POST /v1/sessions
- GET /v1/sessions/{id}
- DELETE /v1/sessions/{id}
- POST /v1/sessions/{id}/messages
- GET /v1/sessions/{id}/messages

#### Worker API

- GET /v1/workers
- GET /v1/workers/{id}
- POST /v1/workers/{id}/restart
- GET /v1/health

### 10.2 Example request

```http
POST /v1/sessions
Content-Type: application/json

{
  "tenant_id": "tenant-1",
  "profile_id": "profile-claude-a",
  "metadata": {"user_id": "u-123"}
}
```

```http
POST /v1/sessions/sess_123/messages
Content-Type: application/json

{
  "message": "Write a Go HTTP server that exposes /health",
  "stream": true,
  "metadata": {"source": "opencode"}
}
```

### 10.3 Streaming response

Use SSE or WebSocket.

```text
event: message
data: {"session_id":"sess_123","worker_id":"worker-7","text":"hello"}
```

## 11. Service components

### 11.1 API Gateway

Responsibilities:

- authn/authz
- request validation
- session creation and status
- proxy to worker
- response streaming
- retry/failover coordination

### 11.2 Scheduler/Router

Responsibilities:

- route request to worker based on session sticky state
- choose worker by load and health
- maintain routing table and session binding

### 11.3 Worker Manager

Responsibilities:

- spawn worker process per profile
- maintain PID and process state
- monitor health every N seconds
- restart dead workers
- maintain resource usage metrics

### 11.4 Claude Worker

Responsibilities:

- run Claude CLI or SDK in isolated environment
- receive message payload from gateway
- stream response chunks back
- maintain conversation/session state

### 11.5 Storage layer

Use a combination of PostgreSQL + Redis for MVP.

- PostgreSQL: profile metadata, sessions, worker records, audit log
- Redis: routing table, sticky session cache, rate limiting, ephemeral state

## 12. Process model and concurrency

- Each profile has exactly one main worker process
- One worker handles many sessions, but each session belongs to one profile
- Many workers can run concurrently in same host or across hosts
- The orchestrator is the central coordinator; workers are passive workers

### 12.1 Concurrency control

- use mutex/lock around worker registration and session binding updates
- use atomic counters for active sessions and queue depth
- no global mutable state without synchronization

## 13. Health checks

### 13.1 Worker health signals

Each worker must report:

- process alive
- heartbeat timestamp
- active sessions count
- queue depth
- CPU/memory usage
- readiness flag

### 13.2 Health check loop

```go
func (wm *WorkerManager) HealthLoop(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            for _, w := range wm.workers {
                if !w.IsHealthy() {
                    wm.RestartWorker(w.ID)
                }
            }
        }
    }
}
```

## 14. Retry and failover

### 14.1 Retry policy

- retry on transient worker errors
- max retries: 3
- exponential backoff: 500ms, 1s, 2s
- only retry when request can be safely replayed

### 14.2 Failover policy

If sticky worker fails:

1. mark worker unhealthy
2. unbind session mapping from dead worker
3. select replacement worker using LB
4. continue session if possible
5. else return 503 / session unavailable

## 15. Security model

### 15.1 AuthN/AuthZ

- client authenticates via token or mTLS
- each request includes tenant_id and profile_id
- route layer checks allowed profile set for caller

### 15.2 Secret handling

- credentials live in secret manager, e.g. Vault / AWS Secrets Manager / GCP Secret Manager
- at startup, worker receives secrets via env injection or mounted files
- do not log secret values

### 15.3 Audit

Every profile create/update/delete and every session binding change should be logged with:

- actor
- tenant
- profile id
- session id
- timestamp
- action

## 16. Observability

### 16.1 Logs

Use structured logs:

```json
{
  "ts":"2026-11-01T10:00:00Z",
  "level":"INFO",
  "request_id":"req_123",
  "tenant_id":"tenant-1",
  "session_id":"sess_123",
  "profile_id":"profile-claude-a",
  "worker_id":"worker-7",
  "event":"route_request",
  "status":"ok"
}
```

### 16.2 Metrics

- requests_total
- request_latency_seconds
- worker_active_sessions
- worker_queue_depth
- worker_health_status
- failover_total
- retry_total
- session_duration_seconds

### 16.3 Tracing

Use OpenTelemetry with spans:

- api.request
- scheduler.select_worker
- worker.invoke
- worker.stream

## 17. Failure modes and recovery

### 17.1 Worker crash

- health checks detect dead worker
- worker manager restarts worker
- session binding is re-established if possible
- user gets consistent retry semantics

### 17.2 Credential expired

- worker health reveals auth failure
- manager re-fetches secret or marks profile unhealthy
- no secret values in logs

### 17.3 High queue load

- scheduler routes new sessions away from overloaded worker
- throttle admission if queue exceeds threshold

### 17.4 Session stuck

- session timeout is enforced
- stale entries are expired
- orphaned sessions cleaned by background job

## 18. Database schema proposal

### 18.1 profiles

| column | type | notes |
| --- | --- | --- |
| id | UUID | PK |
| tenant_id | UUID | tenant owner |
| name | string | profile name |
| login_id | string | provider login key |
| claude_path | string | CLI path |
| status | string | active/disabled |
| home_dir | string | isolated home |
| config_dir | string | isolated config |
| work_dir | string | isolated workspace |
| log_dir | string | isolated logs |
| secret_ref | string | secret manager ref |
| created_at | timestamptz | |
| updated_at | timestamptz | |

### 18.2 workers

| column | type | notes |
| --- | --- | --- |
| id | UUID | PK |
| profile_id | UUID | FK |
| host | string | node host |
| pid | int | process id |
| status | string | running/stopped/unhealthy |
| active_sessions | int | current sessions |
| queue_depth | int | queue |
| cpu_usage | float | |
| memory_usage | bigint | |
| last_heartbeat | timestamptz | |
| created_at | timestamptz | |

### 18.3 sessions

| column | type | notes |
| --- | --- | --- |
| id | UUID | PK |
| tenant_id | UUID | |
| profile_id | UUID | FK |
| worker_id | UUID | nullable |
| status | string | created/running/completed/failed |
| metadata | JSONB | custom metadata |
| created_at | timestamptz | |
| updated_at | timestamptz | |
| expires_at | timestamptz | optional |

### 18.4 session_bindings

| column | type | notes |
| --- | --- | --- |
| session_id | UUID | PK |
| worker_id | UUID | current worker |
| created_at | timestamptz | |
| updated_at | timestamptz | |

## 19. Deployment model

### MVP deployment

- Single host deployment
- Go API service runs as systemd service
- workers run as local child processes with isolated dirs
- PostgreSQL on same host or separate host
- Redis for routing cache

### Production deployment

- multiple worker nodes behind load balancer
- runner as Docker container per profile or per worker
- state distributed across Postgres/Redis
- worker manager runs as background daemon

## 20. Proposed Go package layout

```text
.
├── cmd/
│   └── orchestrator/
│       └── main.go
├── internal/
│   ├── api/
│   │   ├── handlers.go
│   │   ├── router.go
│   │   └── middleware.go
│   ├── config/
│   │   └── config.go
│   ├── orchestrator/
│   │   ├── router.go
│   │   ├── scheduler.go
│   │   └── failover.go
│   ├── worker/
│   │   ├── manager.go
│   │   ├── process.go
│   │   └── health.go
│   ├── session/
│   │   ├── store.go
│   │   └── model.go
│   ├── profile/
│   │   ├── model.go
│   │   └── service.go
│   ├── secret/
│   │   └── provider.go
│   └── observability/
│       ├── logging.go
│       └── metrics.go
├── docs/
│   └── tdd.md
├── go.mod
└── README.md
```

## 21. Risks and mitigations

### Risk 1: Claude process cannot coexist in same home
Mitigation: each profile uses isolated HOME and config dirs.

### Risk 2: auth token collision between profiles
Mitigation: each profile stores own secret in isolated env, not in shared global config.

### Risk 3: worker crashes while handling session
Mitigation: session mapping, health checks, and retry logic.

### Risk 4: queue backlog under high load
Mitigation: scheduling by active sessions and queue length; horizontal scale supported.

### Risk 5: stale session bound to dead worker
Mitigation: periodic heal and failover with session cleanup policy.

## 22. Acceptance criteria

- system can create N isolated profiles with separate environment dirs
- each profile can start exactly one Claude worker process
- session routing is sticky to the bound worker
- on worker failure, failover occurs within a bounded retry window
- API exposes create, list, route, and stream endpoints
- metrics and logs are emitted for all major operations
- secrets are not stored in plaintext logs

## 23. Kết luận

TDD này định nghĩa một orchestrator Go cho multi-profile Claude Agent với isolation, sticky session, and load balancing. Mục tiêu là tách biệt login/profile rõ ràng, giảm xung đột auth/session và cho phép Goose/Opencode client gọi một API thống nhất.

Với kiến trúc này, hệ thống có thể mở rộng từ MVP chạy trên 1 host sang deployment multi-node theo hướng production-ready.

## 24. Next steps

1. khởi tạo Go module và package layout
2. implement config + profile service
3. implement worker manager + health loop
4. implement session store and routing table
5. implement HTTP API endpoints
6. add metrics + tracing
7. add E2E tests for sticky session and failover

---

Tài liệu này là draft kỹ thuật ban đầu cho engineering team và có thể được cập nhật khi bắt đầu implement thật.
