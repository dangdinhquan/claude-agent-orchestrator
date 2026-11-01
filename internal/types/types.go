package api

import (
    "encoding/json"
    "fmt"
    "net/http"
    "strconv"
    "strings"
    "time"

    "github.com/dangdinhquan/claude-agent-orchestrator/internal/config"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/types"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/worker"
)

type Server struct {
    cfg     config.Config
    manager *worker.Manager
    server  *http.Server
}

func NewServer(cfg config.Config) *Server {
    return &Server{
        cfg:     cfg,
        manager: worker.NewManager(cfg),
    }
}

func (s *Server) Start() error {
    mux := http.NewServeMux()
    mux.HandleFunc("/v1/health", s.handleHealth)
    mux.HandleFunc("/v1/profiles", s.handleProfiles)
    mux.HandleFunc("/v1/workers", s.handleWorkers)
    mux.HandleFunc("/v1/sessions", s.handleSessions)
    mux.HandleFunc("/v1/sessions/", s.handleSessionRoutes)

    s.server = &http.Server{
        Addr:           ":" + strconv.Itoa(s.cfg.Port),
        Handler:        mux,
        ReadTimeout:    10 * time.Second,
        WriteTimeout:   30 * time.Second,
        IdleTimeout:    60 * time.Second,
    }

    return s.server.ListenAndServe()
}

func (s *Server) Stop() error {
    if s.server == nil {
        return nil
    }
    return s.server.Close()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    writeJSON(w, s.manager.Health())
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodGet:
        profiles := s.manager.ListProfiles()
        writeJSON(w, profiles)
    case http.MethodPost:
        var req types.CreateProfileRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeError(w, http.StatusBadRequest, "invalid request payload")
            return
        }
        profile, err := s.manager.CreateProfile(req)
        if err != nil {
            writeError(w, http.StatusBadRequest, err.Error())
            return
        }
        writeJSON(w, profile)
    default:
        w.WriteHeader(http.StatusMethodNotAllowed)
    }
}

func (s *Server) handleWorkers(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    writeJSON(w, s.manager.ListWorkers())
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodGet:
        writeJSON(w, s.manager.ListSessions())
    case http.MethodPost:
        var req types.CreateSessionRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeError(w, http.StatusBadRequest, "invalid request payload")
            return
        }
        session, err := s.manager.CreateSession(req)
        if err != nil {
            writeError(w, http.StatusServiceUnavailable, err.Error())
            return
        }
        writeJSON(w, session)
    default:
        w.WriteHeader(http.StatusMethodNotAllowed)
    }
}

func (s *Server) handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
    parts := strings.Split(path, "/")
    if len(parts) == 0 || parts[0] == "" {
        w.WriteHeader(http.StatusNotFound)
        return
    }

    sessionID := parts[0]
    if len(parts) == 1 {
        switch r.Method {
        case http.MethodGet:
            session, ok := s.manager.GetSession(sessionID)
            if !ok {
                writeError(w, http.StatusNotFound, "session not found")
                return
            }
            writeJSON(w, session)
        case http.MethodDelete:
            // Placeholder: session cleanup would be implemented here.
            writeJSON(w, map[string]string{"status": "deleted", "session_id": sessionID})
        default:
            w.WriteHeader(http.StatusMethodNotAllowed)
        }
        return
    }

    if len(parts) == 2 && parts[1] == "messages" {
        switch r.Method {
        case http.MethodPost:
            var req types.MessageRequest
            if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid message payload")
                return
            }

            session, ok := s.manager.GetSession(sessionID)
            if !ok {
                writeError(w, http.StatusNotFound, "session not found")
                return
            }

            workerID := session.WorkerID
            if strings.TrimSpace(workerID) == "" {
                workerID = "unassigned"
            }

            response := map[string]interface{}{
                "session_id": session.ID,
                "worker_id": workerID,
                "status":    "accepted",
                "message":   req.Message,
            }

            if req.Stream {
                w.Header().Set("Content-Type", "text/event-stream")
                w.Header().Set("Cache-Control", "no-cache")
                w.Header().Set("Connection", "keep-alive")
                _, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", mustMarshal(response))
                _, _ = fmt.Fprintf(w, "event: done\ndata: %s\n\n", mustMarshal(map[string]interface{}{"session_id": session.ID, "status": "complete"}))
                return
            }

            writeJSON(w, response)
        default:
            w.WriteHeader(http.StatusMethodNotAllowed)
        }
        return
    }

    w.WriteHeader(http.StatusNotFound)
}

func writeJSON(w http.ResponseWriter, obj interface{}) {
    w.Header().Set("Content-Type", "application/json")
    if err := json.NewEncoder(w).Encode(obj); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}

func writeError(w http.ResponseWriter, status int, msg string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": msg, "status": status})
}

func mustMarshal(v interface{}) string {
    b, err := json.Marshal(v)
    if err != nil {
        return `{}`
    }
    return string(b)
}
