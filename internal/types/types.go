package types

import "time"

type WorkerStatus string

const (
    WorkerRunning  WorkerStatus = "running"
    WorkerStopped  WorkerStatus = "stopped"
    WorkerUnhealthy WorkerStatus = "unhealthy"
)

type Profile struct {
    ID        string            `json:"id"`
    TenantID  string            `json:"tenant_id"`
    Name      string            `json:"name"`
    LoginID   string            `json:"login_id"`
    Status    string            `json:"status"`
    ClaudePath string           `json:"claude_path"`
    HomeDir   string            `json:"home_dir"`
    ConfigDir string            `json:"config_dir"`
    WorkDir   string            `json:"work_dir"`
    LogDir    string            `json:"log_dir"`
    SecretRef string            `json:"secret_ref,omitempty"`
    Env       map[string]string `json:"env,omitempty"`
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`
}

type CreateProfileRequest struct {
    TenantID   string            `json:"tenant_id"`
    Name       string            `json:"name"`
    LoginID    string            `json:"login_id"`
    ClaudePath string            `json:"claude_path,omitempty"`
    Env        map[string]string `json:"env,omitempty"`
}

type Session struct {
    ID        string            `json:"id"`
    TenantID  string            `json:"tenant_id"`
    ProfileID string            `json:"profile_id"`
    WorkerID  string            `json:"worker_id,omitempty"`
    Status    string            `json:"status"`
    Metadata  map[string]string `json:"metadata,omitempty"`
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`
    ExpiresAt *time.Time        `json:"expires_at,omitempty"`
}

type CreateSessionRequest struct {
    TenantID  string            `json:"tenant_id"`
    ProfileID string            `json:"profile_id"`
    Metadata  map[string]string `json:"metadata,omitempty"`
    ID        string            `json:"id,omitempty"`
}

type MessageRequest struct {
    Message string            `json:"message"`
    Stream  bool              `json:"stream"`
    Metadata map[string]string `json:"metadata,omitempty"`
}

type Worker struct {
    ID             string      `json:"id"`
    ProfileID      string      `json:"profile_id"`
    Hostname       string      `json:"hostname"`
    PID            int         `json:"pid"`
    Status         WorkerStatus `json:"status"`
    ActiveSessions  int         `json:"active_sessions"`
    QueueDepth     int         `json:"queue_depth"`
    CPUUsage       float64     `json:"cpu_usage"`
    MemoryUsage    uint64      `json:"memory_usage"`
    LastHeartbeat  time.Time   `json:"last_heartbeat"`
    CreatedAt      time.Time   `json:"created_at"`
    ProcessStateID string      `json:"process_state_id,omitempty"`
}

type HealthStatus struct {
    Status      string    `json:"status"`
    Timestamp   time.Time `json:"timestamp"`
    WorkersTotal int       `json:"workers_total"`
    HealthyWorkers int      `json:"healthy_workers"`
}
