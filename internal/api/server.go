package api

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/config"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/orchestrator"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/types"
)

type Server struct {
    cfg     config.Config
    manager *orchestrator.Manager
    srv     *http.Server
}

func NewServer(cfg config.Config) (*Server, error) {
    manager, err := orchestrator.NewManager(cfg)
    if err != nil {
        return nil, err
    }
    return &Server{cfg: cfg, manager: manager}, nil
}

func (s *Server) Start(ctx context.Context) error {
    r := chi.NewRouter()
    r.Get("/health", s.handleHealth)
    r.Route("/v1", func(r chi.Router) {
        r.Get("/profiles", s.handleListProfiles)
        r.Post("/profiles", s.handleCreateProfile)
        r.Get("/workers", s.handleListWorkers)
        r.Get("/sessions", s.handleListSessions)
        r.Post("/sessions", s.handleCreateSession)
        r.Get("/sessions/{sessionID}", s.handleGetSession)
        r.Post("/sessions/{sessionID}/messages", s.handleMessage)
    })

    s.srv = &http.Server{
        Addr:              fmt.Sprintf(":%d", s.cfg.Port),
        Handler:           r,
        ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout:       10 * time.Second,
        WriteTimeout:      30 * time.Second,
        IdleTimeout:       60 * time.Second,
    }

    go func() {
        <-ctx.Done()
        _ = s.srv.Close()
    }()

    if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        return err
    }
    return nil
}

func (s *Server) Stop() error {
    if s.srv == nil {
        return nil
    }
    return s.srv.Close()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, s.manager.Health())
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, s.manager.ListProfiles())
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, s.manager.ListWorkers())
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, s.manager.ListSessions())
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "sessionID")
    session, ok := s.manager.GetSession(id)
    if !ok {
        writeError(w, http.StatusNotFound, "session not found")
        return
    }
    writeJSON(w, session)
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "sessionID")
    session, ok := s.manager.GetSession(id)
    if !ok {
        writeError(w, http.StatusNotFound, "session not found")
        return
    }

    var req types.MessageRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid message payload")
        return
    }

    payload := map[string]interface{}{
        "session_id": session.ID,
        "worker_id": session.WorkerID,
        "status":    "accepted",
        "message":   req.Message,
    }

    if req.Stream {
        w.Header().Set("Content-Type", "text/event-stream")
        w.Header().Set("Cache-Control", "no-cache")
        w.Header().Set("Connection", "keep-alive")
        w.Header().Set("X-Session-ID", session.ID)
        w.Header().Set("X-Worker-ID", session.WorkerID)

        flusher, ok := w.(http.Flusher)
        if !ok {
            writeError(w, http.StatusInternalServerError, "streaming unsupported")
            return
        }

        _, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", mustJSON(payload))
        flusher.Flush()

        _, _ = fmt.Fprintf(w, "event: done\ndata: %s\n\n", mustJSON(map[string]interface{}{"session_id": session.ID, "status": "complete"}))
        flusher.Flush()
        return
    }

    writeJSON(w, payload)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
    w.Header().Set("Content-Type", "application/json")
    if err := json.NewEncoder(w).Encode(v); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}

func writeError(w http.ResponseWriter, status int, msg string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func mustJSON(v interface{}) string {
    b, err := json.Marshal(v)
    if err != nil {
        return `{}`
    }
    return string(b)
}
