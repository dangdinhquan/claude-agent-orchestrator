package store

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"

    "github.com/dangdinhquan/claude-agent-orchestrator/internal/config"
    _ "modernc.org/sqlite"
)

func Open(cfg config.Config) (*sql.DB, error) {
    if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
        return nil, err
    }

    dbPath := filepath.Join(cfg.DataDir, "orchestrator.db")
    db, err := sql.Open("sqlite", dbPath)
    if err != nil {
        return nil, fmt.Errorf("open sqlite: %w", err)
    }

    if err := db.Ping(); err != nil {
        _ = db.Close()
        return nil, fmt.Errorf("ping sqlite: %w", err)
    }

    if err := migrate(db); err != nil {
        _ = db.Close()
        return nil, err
    }

    return db, nil
}

func migrate(db *sql.DB) error {
    stmts := []string{
        `CREATE TABLE IF NOT EXISTS profiles (
            id TEXT PRIMARY KEY,
            tenant_id TEXT NOT NULL,
            name TEXT NOT NULL,
            login_id TEXT,
            status TEXT NOT NULL,
            claude_path TEXT,
            home_dir TEXT NOT NULL,
            config_dir TEXT NOT NULL,
            work_dir TEXT NOT NULL,
            log_dir TEXT NOT NULL,
            secret_ref TEXT,
            env_json TEXT,
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL
        );`,
        `CREATE TABLE IF NOT EXISTS workers (
            id TEXT PRIMARY KEY,
            profile_id TEXT NOT NULL,
            hostname TEXT NOT NULL,
            pid INTEGER NOT NULL,
            status TEXT NOT NULL,
            active_sessions INTEGER NOT NULL DEFAULT 0,
            queue_depth INTEGER NOT NULL DEFAULT 0,
            cpu_usage REAL NOT NULL DEFAULT 0,
            memory_usage INTEGER NOT NULL DEFAULT 0,
            last_heartbeat TEXT NOT NULL,
            created_at TEXT NOT NULL,
            FOREIGN KEY(profile_id) REFERENCES profiles(id)
        );`,
        `CREATE TABLE IF NOT EXISTS sessions (
            id TEXT PRIMARY KEY,
            tenant_id TEXT NOT NULL,
            profile_id TEXT NOT NULL,
            worker_id TEXT,
            status TEXT NOT NULL,
            metadata_json TEXT,
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            expires_at TEXT,
            FOREIGN KEY(profile_id) REFERENCES profiles(id),
            FOREIGN KEY(worker_id) REFERENCES workers(id)
        );`,
        `CREATE TABLE IF NOT EXISTS session_bindings (
            session_id TEXT PRIMARY KEY,
            worker_id TEXT NOT NULL,
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            FOREIGN KEY(session_id) REFERENCES sessions(id),
            FOREIGN KEY(worker_id) REFERENCES workers(id)
        );`,
        `CREATE TABLE IF NOT EXISTS audit_logs (
            id TEXT PRIMARY KEY,
            actor TEXT,
            tenant_id TEXT,
            profile_id TEXT,
            session_id TEXT,
            action TEXT NOT NULL,
            details TEXT,
            created_at TEXT NOT NULL
        );`,
        `CREATE INDEX IF NOT EXISTS idx_sessions_profile ON sessions(profile_id);`,
        `CREATE INDEX IF NOT EXISTS idx_sessions_worker ON sessions(worker_id);`,
        `CREATE INDEX IF NOT EXISTS idx_workers_profile ON workers(profile_id);`,
    }

    for _, stmt := range stmts {
        if _, err := db.Exec(stmt); err != nil {
            return fmt.Errorf("exec migration: %w", err)
        }
    }

    return nil
}
