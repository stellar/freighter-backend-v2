# Freighter Backend V2 — LLM Quick Start

Evaluate the contributor's machine against all prerequisites for
freighter-backend-v2 (Go), install what's missing, and run the initial setup.

## Step 1: Check all prerequisites

Run every check and collect results. Report all at once.

```bash
# Go >= 1.24.0
go version 2>&1 || which go

# Docker (needed for Redis)
docker --version 2>&1 || which docker

# Docker Compose
docker compose version 2>&1 || which docker-compose

# golangci-lint
golangci-lint --version 2>&1 || which golangci-lint

# Make
make --version 2>&1 | head -1 || which make
```

## Step 2: Present results

```
Freighter Backend V2 — Prerequisites Check
============================================
  Go             1.24.2         >= 1.24.0 required    OK
  Docker         27.x.x         any (for Redis)       OK
  Docker Compose 2.x.x          any                   OK
  golangci-lint  1.x.x          any                   OK
  Make           3.x            any                   OK
```

## Step 3: Install missing tools

Present missing tools and ask the user to confirm before installing.

**Auto-installable (run after user confirms):**

- **Go**: `brew install go` (macOS) or download from [go.dev/dl](https://go.dev/dl/)
- **Docker**: `brew install --cask docker` (macOS) or follow
  [docs.docker.com](https://docs.docker.com/engine/install/) (Linux)
- **golangci-lint**: `brew install golangci-lint` (macOS) or
  `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`

## Step 4: Configure

Check if `configs/.toml` exists. If not:

```bash
cp configs/.toml-EXAMPLE configs/.toml
```

Set these for local development:

| Variable                 | Value                                    |
| ------------------------ | ---------------------------------------- |
| `FREIGHTER_BACKEND_PORT` | `3002`                                   |
| `FREIGHTER_BACKEND_HOST` | `localhost`                              |
| `MODE`                   | `development`                            |
| `TESTNET_RPC_URL`        | `https://soroban-testnet.stellar.org`    |
| `PUBNET_RPC_URL`         | `https://soroban.stellar.org` (or `not-set`) |
| `FUTURENET_RPC_URL`      | `not-set` (unless testing futurenet)     |
| `REDIS_HOST`             | `localhost`                              |
| `REDIS_PORT`             | `6379`                                   |

Other variables can stay as `not-set` for local dev.

## Step 5: Run initial setup

```bash
# Start Redis
docker compose -f deployments/docker-compose.yml up -d redis

# Build and run
make build
make run
```

## Step 6: Verify

```bash
make check              # All quality checks
make unit-test          # Unit tests
make unit-test-coverage # Generate local coverage report (80% threshold enforced in CI)
```

## Step 7: Summary

```
Setup Complete
==============
  Prerequisites: [list with versions]
  Configured: configs/.toml from .toml-EXAMPLE

  Ready to run:
  1. docker compose -f deployments/docker-compose.yml up -d redis  (start Redis)
  2. make run                                                       (start server)
```
