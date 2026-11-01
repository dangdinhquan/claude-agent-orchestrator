package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/dangdinhquan/claude-agent-orchestrator/internal/api"
    "github.com/dangdinhquan/claude-agent-orchestrator/internal/config"
)

func main() {
    cfg := config.Load()
    srv, err := api.NewServer(cfg)
    if err != nil {
        log.Fatalf("create server: %v", err)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go func() {
        sigs := make(chan os.Signal, 1)
        signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
        <-sigs
        log.Println("received shutdown signal")
        cancel()
        if err := srv.Stop(); err != nil {
            log.Printf("stop server: %v", err)
        }
    }()

    log.Printf("starting orchestrator on :%d", cfg.Port)
    if err := srv.Start(ctx); err != nil {
        log.Printf("server exited: %v", err)
    }

    time.Sleep(100 * time.Millisecond)
}
