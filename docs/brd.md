# BRD: Multi-Agent Claude Orchestrator

## 1. Mục tiêu
Xây dựng hệ thống orchestration quản lý nhiều profile Claude agent độc lập trong một nền tảng Go, cho phép:
- tạo nhiều profile người dùng/tenant
- chạy worker riêng cho từng profile
- route session theo sticky worker
- theo dõi health và failover
- đảm bảo isolation về filesystem, env, và runtime per profile

## 2. Scope
### 2.1 In scope
- backend orchestration service
- profile lifecycle management
- worker process management
- session routing
- health monitoring
- SQLite persistence
- HTTP API
- SSE streaming for messages

### 2.2 Out of scope
- full auth/authorization enterprise
- real Claude API provider integration
- GUI dashboard
- distributed multi-node cluster

## 3. Non-functional requirements
- isolation: mỗi profile có thư mục riêng cho home/config/workspace/log
- reliability: worker unhealthy phải bị loại khỏi routing
- observability: health endpoint, logs, metrics basic
- portability: chạy trên Linux/macOS, tối thiểu phải compile trên Go 1.22
- simplicity: không dùng framework nặng ngoài routing basic

## 4. Use cases
### 4.1 Create profile
Input: tenant_id, name, login_id, optional env, optional claude_path
Expected:
- tạo thư mục root/profile_id/{home,config,workspace,logs}
- tạo worker process cho profile
- lưu profile vào DB
- trả về profile details

### 4.2 Create session
Input: tenant_id, profile_id, optional metadata, optional session ID
Expected:
- validate profile tồn tại
- chọn worker khỏe mạnh tối ưu nhất
- gán session vào worker
- lưu session vào DB
- trả về session data

### 4.3 Send message to session
Input: session_id, message, stream flag
Expected:
- session tồn tại
- route đến worker đúng
- trả SSE nếu stream=true
- trả JSON nếu stream=false

### 4.4 Health check
Expected:
- trả tổng số worker, số worker khỏe mạnh
- có cơ chế health loop để phát hiện dead worker

## 5. Functional requirements
FR-01: Hệ thống phải hỗ trợ nhiều tenant.
FR-02: Mỗi tenant có thể tạo nhiều profile.
FR-03: Mỗi profile phải có một worker runtime riêng.
FR-04: Mỗi session được route đến đúng worker.
FR-05: Health loop phải cập nhật trạng thái worker.
FR-06: API phải cung cấp endpoints CRUD cơ bản cho profile/session/worker.
FR-07: DB phải có persistence cho profile/session/worker.
FR-08: Logs phải ghi theo từng profile.

## 6. API contract
### Health
GET /health
Response:
{
  "status": "ok",
  "workers_total": 2,
  "workers_healthy": 2,
  "sessions_total": 4,
  "time": "2026-11-01T00:00:00Z"
}

### Profile list
GET /v1/profiles
### Create profile
POST /v1/profiles
Body:
{
  "tenant_id": "tenant-1",
  "name": "qa-agent",
  "login_id": "user-1",
  "claude_path": "/usr/local/bin/claude",
  "env": {
    "MODEL": "claude-sonnet-4"
  }
}

### Session list
GET /v1/sessions
### Create session
POST /v1/sessions
Body:
{
  "tenant_id": "tenant-1",
  "profile_id": "profile-123",
  "metadata": {
    "task": "review PR"
  }
}

### Session detail
GET /v1/sessions/{sessionID}
### Send message
POST /v1/sessions/{sessionID}/messages
Body:
{
  "message": "please inspect repo",
  "stream": true
}

## 7. Data model
### Profile
- id
- tenant_id
- name
- login_id
- status
- claude_path
- home_dir
- config_dir
- work_dir
- log_dir
- secret_ref
- env
- created_at
- updated_at

### Worker
- id
- profile_id
- hostname
- pid
- status
- active_sessions
- queue_depth
- cpu_usage
- memory_usage
- last_heartbeat
- created_at

### Session
- id
- tenant_id
- profile_id
- worker_id
- status
- metadata
- created_at
- updated_at
- expires_at

## 8. Architecture
- `cmd/orchestrator` entry point
- `internal/api` HTTP handlers and routing
- `internal/orchestrator` business logic
- `internal/store` SQLite persistence
- `internal/types` shared DTOs
- `internal/config` config loader

## 9. Risk and assumptions
- Real Claude binary path may vary across machines
- Health checks must be conservative to avoid false positives
- Session routing is sticky per session ID and fallback on worker health
- External secret management is out of initial scope

## 10. Definition of done
The system is considered done when:
- build passes with `go build ./...`
- API routes respond correctly
- profile/session/worker can be created and listed
- sticky session routing works
- worker health updates correctly
- SQLite persists data across restart
- each profile has isolated runtime directory structure
