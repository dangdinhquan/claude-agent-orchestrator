package worker

import (
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
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/types"
)

var (
    ErrNotFound       = errors.New("not found")
    ErrNoHealthyWorker = errors.New("no healthy worker available")
)

// Manager is the central orchestrator for profiles, workers and sessions.
type Manager struct {
    mu       sync.RWMutex
    cfg      config.Config
    profiles map[string]*types.Profile
    workers  map[string]*types.Worker
    sessions map[string]*types.Session
    routes   map[string]string // session ID -> worker ID
}

func NewManager(cfg config.Config) *Manager {
    if err := os.MkdirAll(cfg.ProfileRoot(), 0o755); err != nil {
        panic(err)
    }
    if err := os.MkdirAll(cfg.WorkerRoot(), 0o755); err != nil {
        panic(err)
    }

    return &Manager{
        cfg:      cfg,
        profiles: make(map[string]*types.Profile),
        workers:  make(map[string]*types.Worker),
        sessions: make(map[string]*types.Session),
        routes:   make(map[string]string),
    }
}

func (m *Manager) CreateProfile(req types.CreateProfileRequest) (*types.Profile, error) {
    if strings.TrimSpace(req.Name) == "" {
        return nil, errors.New("profile name is required")
    }
    if strings.TrimSpace(req.TenantID) == "" {
        return nil, errors.New("tenant_id is required")
    }

    profileID := generateID("profile")
    profileRoot := filepath.Join(m.cfg.ProfileRoot(), profileID)
    homeDir := filepath.Join(profileRoot, "home")
    configDir := filepath.Join(profileRoot, "config")
    workDir := filepath.Join(profileRoot, "workspace")
    logDir := filepath.Join(profileRoot, "logs")

    for _, dir := range []string{homeDir, configDir, workDir, logDir} {
        if err := os.MkdirAll(dir, 0o755); err != nil {
            return nil, err
        }
    }

    now := time.Now()
    profile := &types.Profile{
        ID:        profileID,
        TenantID:  req.TenantID,
        Name:      req.Name,
        LoginID:   req.LoginID,
        Status:    "active",
        ClaudePath: firstNonEmpty(req.ClaudePath, m.cfg.DefaultClaudePath),
        HomeDir:   homeDir,
        ConfigDir: configDir,
        WorkDir:   workDir,
        LogDir:    logDir,
        Env:       req.Env,
        CreatedAt: now,
        UpdatedAt: now,
    }

    m.mu.Lock()
    m.profiles[profile.ID] = profile
    m.mu.Unlock()

    if _, err := m.StartWorker(profile); err != nil {
        return nil, err
    }

    return profile, nil
}

func (m *Manager) ListProfiles() []*types.Profile {
    m.mu.RLock()
    defer m.mu.RUnlock()

    profiles := make([]*types.Profile, 0, len(m.profiles))
    for _, p := range m.profiles {
        profiles = append(profiles, p)
    }
    sort.Slice(profiles, func(i, j int) bool {
        return profiles[i].CreatedAt.Before(profiles[j].CreatedAt)
    })
    return profiles
}

func (m *Manager) GetProfile(id string) (*types.Profile, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    p, ok := m.profiles[id]
    return p, ok
}

func (m *Manager) CreateSession(req types.CreateSessionRequest) (*types.Session, error) {
    if strings.TrimSpace(req.TenantID) == "" {
        return nil, errors.New("tenant_id is required")
    }
    if strings.TrimSpace(req.ProfileID) == "" {
        return nil, errors.New("profile_id is required")
    }
    if _, ok := m.profiles[req.ProfileID]; !ok {
        return nil, fmt.Errorf("profile %s not found", req.ProfileID)
    }

    sessionID := req.ID
    if strings.TrimSpace(sessionID) == "" {
        sessionID = generateID("session")
    }

    worker, err := m.SelectWorker(sessionID)
    if err != nil {
        return nil, err
    }

    now := time.Now()
    session := &types.Session{
        ID:        sessionID,
        TenantID:  req.TenantID,
        ProfileID: req.ProfileID,
        WorkerID:  worker.ID,
        Status:    "active",
        Metadata:  req.Metadata,
        CreatedAt: now,
        UpdatedAt: now,
    }

    m.mu.Lock()
    m.sessions[session.ID] = session
    m.routes[session.ID] = worker.ID
    m.mu.Unlock()

    m.mu.RLock()
    if w, ok := m.workers[worker.ID]; ok {
        w.ActiveSessions++
    }
    m.mu.RUnlock()

    return session, nil
}

func (m *Manager) GetSession(id string) (*types.Session, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    s, ok := m.sessions[id]
    return s, ok
}

func (m *Manager) ListWorkers() []*types.Worker {
    m.mu.RLock()
    defer m.mu.RUnlock()

    workers := make([]*types.Worker, 0, len(m.workers))
    for _, w := range m.workers {
        workers = append(workers, w)
    }
    sort.Slice(workers, func(i, j int) bool {
        return workers[i].CreatedAt.Before(workers[j].CreatedAt)
    })
    return workers
}

func (m *Manager) SelectWorker(sessionID string) (*types.Worker, error) {
    m.mu.RLock()
    if workerID, ok := m.routes[sessionID]; ok {
        if worker, exists := m.workers[workerID]; exists && worker.IsHealthy() {
            m.mu.RUnlock()
            return worker, nil
        }
    }
    m.mu.RUnlock()

    m.mu.RLock()
    candidates := make([]*types.Worker, 0, len(m.workers))
    for _, worker := range m.workers {
        if worker.IsHealthy() {
            candidates = append(candidates, worker)
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
    cmd := exec.Command(m.cfg.DefaultClaudePath, "-lc", "sleep 3600")
    cmd.Env = append(os.Environ(), "HOME="+profile.HomeDir, "XDG_CONFIG_HOME="+profile.ConfigDir)
    for key, value := range profile.Env {
        cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
    }
    cmd.Dir = profile.WorkDir

    if err := cmd.Start(); err != nil {
        return nil, err
    }

    worker := &types.Worker{
        ID:            generateID("worker"),
        ProfileID:     profile.ID,
        Hostname:      hostName(),
        PID:           cmd.Process.Pid,
        Status:        types.WorkerRunning,
        ActiveSessions: 0,
        QueueDepth:    0,
        LastHeartbeat: time.Now(),
        CreatedAt:     time.Now(),
        Cmd:           cmd,
    }

    m.mu.Lock()
    m.workers[worker.ID] = worker
    m.mu.Unlock()

    go func() {
        _ = cmd.Wait()
        m.mu.Lock()
        if current, ok := m.workers[worker.ID]; ok {
            current.Status = types.WorkerStopped
            current.LastHeartbeat = time.Now()
        }
        m.mu.Unlock()
    }()

    return worker, nil
}

func (m *Manager) ListSessions() []*types.Session {
    m.mu.RLock()
    defer m.mu.RUnlock()

    sessions := make([]*types.Session, 0, len(m.sessions))
    for _, s := range m.sessions {
        sessions = append(sessions, s)
    }
    sort.Slice(sessions, func(i, j int) bool {
        return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
    })
    return sessions
}

func (m *Manager) Health() map[string]interface{} {
    m.mu.RLock()
    defer m.mu.RUnlock()

    healthy := 0
    for _, w := range m.workers {
        if w.IsHealthy() {
            healthy++
        }
    }

    return map[string]interface{}{
        "status": "ok",
        "workers_total": len(m.workers),
        "workers_healthy": healthy,
        "sessions_total": len(m.sessions),
        "time": time.Now().UTC(),
    }
}

func hostName() string {
    host, err := os.Hostname()
    if err != nil {
        return "localhost"
    }
    return host
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

func (w *types.Worker) IsHealthy() bool {
    if w == nil {
        return false
    }
    if w.Status == types.WorkerStopped || w.Status == types.WorkerUnhealthy {
        return false
    }
    if w.Cmd == nil || w.Cmd.Process == nil {
        return false
    }
    return w.Cmd.Process.Signal(os.Interrupt) == nil
}
