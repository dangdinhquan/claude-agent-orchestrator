package config

import (
    "fmt"
    "os"
    "strconv"
    "time"
)

const defaultPort = 8080

type Config struct {
    Port              int
    DataDir           string
    HealthInterval    time.Duration
    DefaultClaudePath string
    DefaultShell      string
}

func Load() Config {
    port, _ := strconv.Atoi(os.Getenv("ORCHESTRATOR_PORT"))
    if port == 0 {
        port = defaultPort
    }

    dataDir := os.Getenv("ORCHESTRATOR_DATA_DIR")
    if dataDir == "" {
        dataDir = "/tmp/claude-agent-orchestrator"
    }

    interval, err := time.ParseDuration(os.Getenv("ORCHESTRATOR_HEALTH_INTERVAL"))
    if err != nil || interval <= 0 {
        interval = 5 * time.Second
    }

    claudePath := os.Getenv("ORCHESTRATOR_CLAUDE_PATH")
    if claudePath == "" {
        claudePath = "/bin/sh"
    }

    shell := os.Getenv("ORCHESTRATOR_SHELL")
    if shell == "" {
        shell = "/bin/sh"
    }

    return Config{
        Port:              port,
        DataDir:           dataDir,
        HealthInterval:    interval,
        DefaultClaudePath: claudePath,
        DefaultShell:      shell,
    }
}

func (c Config) ProfileRoot() string {
    return fmt.Sprintf("%s/profiles", c.DataDir)
}

func (c Config) WorkerRoot() string {
    return fmt.Sprintf("%s/workers", c.DataDir)
}
