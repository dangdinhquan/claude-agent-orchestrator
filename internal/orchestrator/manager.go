package orchestrator

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"

    "github.com/dangdinhquan/claude-agent-orchestrator/internal/config"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/store"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/types"
)

var ErrNoHealthyWorker = errors.New("no healthy worker available")

type Manager struct {
    mu       sync.RWMutex
    cfg      config.Config
    db       *sql.DB
    profiles map[string]*types.Profile
    workers  map[string]*types.Worker
    sessions map[string]*types.Session
}

func NewManager(cfg config.Config) (*Manager, error) {
    db, err := store.Open(cfg)
    if err != nil {
        return nil, err
    }

    m := &Manager{cfg: cfg, db: db, profiles: map[string]*types.Profile{}, workers: map[string]*types.Worker{}, sessions: map[string]*types.Session{}}
    if err := m.Load(); err != nil {
        _ = db.Close()
        return nil, err
    }
    return m, nil
}

func (m *Manager) Close() error {
    if m == nil || m.db == nil {
        return nil
    }
    return m.db.Close()
}

func (m *Manager) Load() error {
    profiles, err := m.listProfilesFromDB()
    if err != nil {
        return err
    }
    workers, err := m.listWorkersFromDB()
    if err != nil {
        return err
    }
    sessions, err := m.listSessionsFromDB()
    if err != nil {
        return err
    }

    m.mu.Lock()
    defer m.mu.Unlock()
    m.profiles = profiles
    m.workers = workers
    m.sessions = sessions
    return nil
}

func (m *Manager) CreateProfile(req types.CreateProfileRequest) (*types.Profile, error) {
    if strings.TrimSpace(req.Name) == "" {
        return nil, errors.New("profile name is required")
    }
    if strings.TrimSpace(req.TenantID) == "" {
        return nil, errors.New("tenant_id is required")
    }

    id := generateID("profile")
    root := filepath.Join(m.cfg.ProfileRoot(), id)
    dirs := []string{filepath.Join(root, "home"), filepath.Join(root, "config"), filepath.Join(root, "workspace"), filepath.Join(root, "logs")}
    for _, d := range dirs {
        if err := os.MkdirAll(d, 0o755); err != nil {
            return nil, err
        }
    }

    now := time.Now().UTC()
    p := &types.Profile{
        ID:        id,
        TenantID:  req.TenantID,
        Name:      req.Name,
        LoginID:   req.LoginID,
        Status:    "active",
        ClaudePath: firstNonEmpty(req.ClaudePath, m.cfg.DefaultClaudePath),
        HomeDir:   dirs[0],
        ConfigDir: dirs[1],
        WorkDir:   dirs[2],
        LogDir:    dirs[3],
        Env:       req.Env,
        CreatedAt: now,
        UpdatedAt: now,
    }

    envJSON, err := json.Marshal(p.Env)
    if err != nil {
        return nil, err
    }

    _, err = m.db.Exec(
        `INSERT INTO profiles (id, tenant_id, name, login_id, status, claude_path, home_dir, config_dir, work_dir, log_dir, secret_ref, env_json, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        p.ID, p.TenantID, p.Name, p.LoginID, p.Status, p.ClaudePath, p.HomeDir, p.ConfigDir, p.WorkDir, p.LogDir, p.SecretRef, string(envJSON), p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339),
    )
    if err != nil {
        return nil, err
    }

    if _, err := m.StartWorker(p); err != nil {
        return nil, err
    }

    m.mu.Lock()
    m.profiles[p.ID] = p
    m.mu.Unlock()

    return p, nil
}

func (m *Manager) ListProfiles() []*types.Profile {
    m.mu.RLock(); defer m.mu.RUnlock()
    out := make([]*types.Profile, 0, len(m.profiles))
    for _, p := range m.profiles {
        out = append(out, p)
    }
    sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
    return out
}

func (m *Manager) GetProfile(id string) (*types.Profile, bool) {
    m.mu.RLock(); defer m.mu.RUnlock()
    p, ok := m.profiles[id]
    return p, ok
}

func (m *Manager) CreateSession(req types.CreateSessionRequest) (*types.Session, error) {
    if req.TenantID == "" {
        return nil, errors.New("tenant_id is required")
    }
    if req.ProfileID == "" {
        return nil, errors.New("profile_id is required")
    }
    if _, ok := m.profiles[req.ProfileID]; !ok {
        return nil, fmt.Errorf("profile %s not found", req.ProfileID)
    }

    sessionID := req.ID
    if sessionID == "" {
        sessionID = generateID("session")
    }

    worker, err := m.SelectWorker(sessionID)
    if err != nil {
        return nil, err
    }

    now := time.Now().UTC()
    s := &types.Session{ID: sessionID, TenantID: req.TenantID, ProfileID: req.ProfileID, WorkerID: worker.ID, Status: "active", Metadata: req.Metadata, CreatedAt: now, UpdatedAt: now}

    metaJSON, err := json.Marshal(s.Metadata)
    if err != nil {
        return nil, err
    }

    _, err = m.db.Exec(
        `INSERT INTO sessions (id, tenant_id, profile_id, worker_id, status, metadata_json, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        s.ID, s.TenantID, s.ProfileID, s.WorkerID, s.Status, string(metaJSON), s.CreatedAt.Format(time.RFC3339), s.UpdatedAt.Format(time.RFC3339), nil,
    )
    if err != nil {
        return nil, err
    }

    m.mu.Lock()
    m.sessions[s.ID] = s
    m.mu.Unlock()

    if _, err := m.db.Exec(`UPDATE workers SET active_sessions = active_sessions + 1 WHERE id = ?`, worker.ID); err != nil {
        return nil, err
    }

    m.mu.Lock()
    if w, ok := m.workers[worker.ID]; ok {
        w.ActiveSessions++
    }
    m.mu.Unlock()

    return s, nil
}

func (m *Manager) ListSessions() []*types.Session {
    m.mu.RLock(); defer m.mu.RUnlock()
    out := make([]*types.Session, 0, len(m.sessions))
    for _, s := range m.sessions { out = append(out, s) }
    sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
    return out
}

func (m *Manager) GetSession(id string) (*types.Session, bool) {
    m.mu.RLock(); defer m.mu.RUnlock()
    s, ok := m.sessions[id]
    return s, ok
}

func (m *Manager) ListWorkers() []*types.Worker {
    m.mu.RLock(); defer m.mu.RUnlock()
    out := make([]*types.Worker, 0, len(m.workers))
    for _, w := range m.workers { out = append(out, w) }
    sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
    return out
}

func (m *Manager) SelectWorker(sessionID string) (*types.Worker, error) {
    m.mu.RLock()
    if s, ok := m.sessions[sessionID]; ok {
        if w, exists := m.workers[s.WorkerID]; exists && w.IsHealthy() {
            m.mu.RUnlock()
            return w, nil
        }
    }

    candidates := make([]*types.Worker, 0, len(m.workers))
    for _, w := range m.workers {
        if w.IsHealthy() {
            candidates = append(candidates, w)
        }
    }
    m.mu.RUnlock()

    if len(candidates) == 0 {
        return nil, ErrNoHealthyWorker
    }

    sort.Slice(candidates, func(i, j int) bool {
        if candidates[i].QueueDepth != candidates[j].QueueDepth {
            return candidates[i].QueueDepth < candidates[j].QueueDepth
        }
        if candidates[i].ActiveSessions != candidates[j].ActiveSessions {
            return candidates[i].ActiveSessions < candidates[j].ActiveSessions
        }
        return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
    })

    return candidates[0], nil
}

func (m *Manager) StartWorker(profile *types.Profile) (*types.Worker, error) {
    cmd := exec.Command(profile.ClaudePath, "-lc", "sleep 3600")
    env := append(os.Environ(), "HOME="+profile.HomeDir, "XDG_CONFIG_HOME="+profile.ConfigDir)
    for k, v := range profile.Env {
        env = append(env, fmt.Sprintf("%s=%s", k, v))
    }
    cmd.Env = env
    cmd.Dir = profile.WorkDir

    logfile := filepath.Join(profile.LogDir, "worker.log")
    f, err := os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
    if err != nil {
        return nil, err
    }
    cmd.Stdout = f
    cmd.Stderr = f

    if err := cmd.Start(); err != nil {
        _ = f.Close()
        return nil, err
    }

    now := time.Now().UTC()
    w := &types.Worker{
        ID:            generateID("worker"),
        ProfileID:     profile.ID,
        Hostname:      hostname(),
        PID:           cmd.Process.Pid,
        Status:        types.WorkerRunning,
        ActiveSessions: 0,
        QueueDepth:    0,
        LastHeartbeat: now,
        CreatedAt:     now,
    }

    _, err = m.db.Exec(
        `INSERT INTO workers (id, profile_id, hostname, pid, status, active_sessions, queue_depth, cpu_usage, memory_usage, last_heartbeat, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        w.ID, w.ProfileID, w.Hostname, w.PID, string(w.Status), w.ActiveSessions, w.QueueDepth, w.CPUUsage, w.MemoryUsage, w.LastHeartbeat.Format(time.RFC3339), w.CreatedAt.Format(time.RFC3339),
    )
    if err != nil {
        _ = f.Close()
        return nil, err
    }

    m.mu.Lock(); m.workers[w.ID] = w; m.mu.Unlock()

    go func() {
        _ = cmd.Wait()
        m.markWorkerStopped(w.ID)
        _ = f.Close()
    }()

    return w, nil
}

func (m *Manager) markWorkerStopped(workerID string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if w, ok := m.workers[workerID]; ok {
        w.Status = types.WorkerStopped
        w.LastHeartbeat = time.Now().UTC()
    }
    _, _ = m.db.Exec(`UPDATE workers SET status = ?, last_heartbeat = ? WHERE id = ?`, string(types.WorkerStopped), time.Now().UTC().Format(time.RFC3339), workerID)
}

func (m *Manager) Health() map[string]interface{} {
    m.mu.RLock(); defer m.mu.RUnlock()
    healthy := 0
    for _, w := range m.workers {
        if w.IsHealthy() { healthy++ }
    }
    return map[string]interface{}{"status": "ok", "workers_total": len(m.workers), "workers_healthy": healthy, "sessions_total": len(m.sessions), "time": time.Now().UTC()}
}

func (m *Manager) RunHealthLoop(ctx context.Context) {
    ticker := time.NewTicker(m.cfg.HealthInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            m.refreshHealth()
        }
    }
}

func (m *Manager) refreshHealth() {
    m.mu.Lock(); defer m.mu.Unlock()
    for _, w := range m.workers {
        if w == nil || w.ProcessStateID == "" { continue }
        // process is assumed healthy if still present; can be replaced with PID/process state management in real integration.
        if w.Status != types.WorkerRunning {
            w.Status = types.WorkerRunning
        }
        w.LastHeartbeat = time.Now().UTC()
        _, _ = m.db.Exec(`UPDATE workers SET status = ?, last_heartbeat = ? WHERE id = ?`, string(w.Status), w.LastHeartbeat.Format(time.RFC3339), w.ID)
    }
}

func (m *Manager) listProfilesFromDB() (map[string]*types.Profile, error) {
    rows, err := m.db.Query(`SELECT id, tenant_id, name, login_id, status, claude_path, home_dir, config_dir, work_dir, log_dir, secret_ref, env_json, created_at, updated_at FROM profiles`)
    if err != nil { return nil, err }
    defer rows.Close()

    out := map[string]*types.Profile{}
    for rows.Next() {
        var p types.Profile
        var createdAt, updatedAt, envJSON string
        if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.LoginID, &p.Status, &p.ClaudePath, &p.HomeDir, &p.ConfigDir, &p.WorkDir, &p.LogDir, &p.SecretRef, &envJSON, &createdAt, &updatedAt); err != nil {
            return nil, err
        }
        if envJSON != "" {
            var env map[string]string
            if err := json.Unmarshal([]byte(envJSON), &env); err == nil {
                p.Env = env
            }
        }
        if t, err := time.Parse(time.RFC3339, createdAt); err == nil { p.CreatedAt = t }
        if t, err := time.Parse(time.RFC3339, updatedAt); err == nil { p.UpdatedAt = t }
        out[p.ID] = &p
    }
    return out, rows.Err()
}

func (m *Manager) listWorkersFromDB() (map[string]*types.Worker, error) {
    rows, err := m.db.Query(`SELECT id, profile_id, hostname, pid, status, active_sessions, queue_depth, cpu_usage, memory_usage, last_heartbeat, created_at FROM workers`)
    if err != nil { return nil, err }
    defer rows.Close()

    out := map[string]*types.Worker{}
    for rows.Next() {
        var w types.Worker
        var status, heartbeat, createdAt string
        if err := rows.Scan(&w.ID, &w.ProfileID, &w.Hostname, &w.PID, &status, &w.ActiveSessions, &w.QueueDepth, &w.CPUUsage, &w.MemoryUsage, &heartbeat, &createdAt); err != nil {
            return nil, err
        }
        w.Status = types.WorkerStatus(status)
        if t, err := time.Parse(time.RFC3339, heartbeat); err == nil { w.LastHeartbeat = t }
        if t, err := time.Parse(time.RFC3339, createdAt); err == nil { w.CreatedAt = t }
        out[w.ID] = &w
    }
    return out, rows.Err()
}

func (m *Manager) listSessionsFromDB() (map[string]*types.Session, error) {
    rows, err := m.db.Query(`SELECT id, tenant_id, profile_id, worker_id, status, metadata_json, created_at, updated_at, expires_at FROM sessions`)
    if err != nil { return nil, err }
    defer rows.Close()

    out := map[string]*types.Session{}
    for rows.Next() {
        var s types.Session
        var metadataJSON, createdAt, updatedAt, expiresAt sql.NullString
        if err := rows.Scan(&s.ID, &s.TenantID, &s.ProfileID, &s.WorkerID, &s.Status, &metadataJSON, &createdAt, &updatedAt, &expiresAt); err != nil {
            return nil, err
        }
        if metadataJSON.Valid && metadataJSON.String != "" {
            var metadata map[string]string
            if err := json.Unmarshal([]byte(metadataJSON.String), &metadata); err == nil { s.Metadata = metadata }
        }
        if createdAt.Valid { if t, err := time.Parse(time.RFC3339, createdAt.String); err == nil { s.CreatedAt = t } }
        if updatedAt.Valid { if t, err := time.Parse(time.RFC3339, updatedAt.String); err == nil { s.UpdatedAt = t } }
        if expiresAt.Valid { if t, err := time.Parse(time.RFC3339, expiresAt.String); err == nil { s.ExpiresAt = &t } }
        out[s.ID] = &s
    }
    return out, rows.Err()
}

func generateID(prefix string) string {
    return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func firstNonEmpty(values ...string) string {
    for _, v := range values {
        if strings.TrimSpace(v) != "" {
            return v
        }
    }
    return ""
}

func hostname() string {
    host, err := os.Hostname(); if err != nil { return "localhost" }
    return host
}

func (w *types.Worker) IsHealthy() bool {
    if w == nil {
        return false
    }
    if w.Status == types.WorkerStopped || w.Status == types.WorkerUnhealthy {
        return false
    }
    return true
}
