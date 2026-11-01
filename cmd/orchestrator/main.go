package main

import (
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/dangdinhquan/claude-agent-orchestrator/internal/api"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/config"
)

func main() {
    cfg := config.Load()
    srv := api.NewServer(cfg)

    go func() {
        log.Printf("starting orchestrator on :%d", cfg.Port)
        if err := srv.Start(); err != nil {
            log.Fatalf("server error: %v", err)
        }
    }()

    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    <-sigs

    log.Println("shutting down orchestrator")
    _ = srv.Stop()
}
