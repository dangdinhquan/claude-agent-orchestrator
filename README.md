# Claude Agent Orchestrator

A Go-based orchestration service for managing multiple isolated Claude Agent profiles, with sticky sessions and load balancing.

## Overview

This project provides a minimal production-ready skeleton for:

- creating isolated Claude profiles
- starting one worker process per profile
- tracking sessions and worker health
- routing requests by sticky session
- balancing traffic across healthy workers
- exposing a Go HTTP API for clients like opencode

## Run

```bash
go run ./cmd/orchestrator
```

By default the API listens on:

- `:8080`

## API examples

### Create profile

```bash
curl -X POST http://localhost:8080/v1/profiles \
  -H 'Content-Type: application/json' \
  -d '{
    "tenant_id": "tenant-1",
    "name": "profile-a",
    "login_id": "login-a",
    "claude_path": "/bin/sh",
    "env": {
      "PROFILE_ID": "profile-a"
    }
  }'
```

### Create session

```bash
curl -X POST http://localhost:8080/v1/sessions \
  -H 'Content-Type: application/json' \
  -d '{
    "tenant_id": "tenant-1",
    "profile_id": "<profile-id>",
    "metadata": {"user_id": "user-1"}
  }'
```

### Send message

```bash
curl -X POST http://localhost:8080/v1/sessions/<session_id>/messages \
  -H 'Content-Type: application/json' \
  -d '{
    "message": "hello from opencode",
    "stream": true
  }'
```

## Notes

This is a framework skeleton rather than a full production Claude SDK implementation. It demonstrates the required orchestration architecture and integration points.
