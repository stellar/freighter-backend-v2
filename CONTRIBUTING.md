# Contributing to Freighter Backend V2

Go backend service for the Freighter wallet. Provides collectibles, RPC health
checks, ledger key accounts, protocol info, and Blockaid scanning.

For the Stellar organization's general contribution guidelines, see the
[Stellar Contribution Guide](https://github.com/stellar/.github/blob/master/CONTRIBUTING.md).

## Prerequisites

| Tool          | Version   | Install                                                            |
| ------------- | --------- | ------------------------------------------------------------------ |
| Go            | >= 1.24.0 | [go.dev/dl](https://go.dev/dl/) (toolchain 1.24.2)                |
| Docker        | Latest    | [docker.com](https://docs.docker.com/get-docker/) (for Redis)     |
| golangci-lint | Latest    | `brew install golangci-lint` or [docs](https://golangci-lint.run/) |

## Getting Started

### Quick Setup with an LLM

If you use an LLM-powered coding assistant, you can automate the setup. The repo
includes a quick start guide ([`LLM-QUICK-START.md`](LLM-QUICK-START.md)) that
checks your environment, installs missing tools, configures the app, and verifies
the build.

Point your LLM assistant at `LLM-QUICK-START.md` and ask it to follow the steps.

If you don't use an LLM assistant, follow the manual setup below.

### Manual Setup

```bash
git clone https://github.com/stellar/freighter-backend-v2.git
cd freighter-backend-v2
cp configs/.toml-EXAMPLE configs/.toml    # Then fill in values (see below)
docker compose -f deployments/docker-compose.yml up -d  # Start Redis
make build
make run
```

### Configuration

Copy `configs/.toml-EXAMPLE` to `configs/.toml`. For local development:

**Required:**

| Variable                 | Value for local dev                                  |
| ------------------------ | ---------------------------------------------------- |
| `FREIGHTER_BACKEND_PORT` | `3002`                                               |
| `FREIGHTER_BACKEND_HOST` | `localhost`                                          |
| `MODE`                   | `development`                                        |
| `RPC_URL`                | A Stellar RPC endpoint (e.g., `https://soroban-testnet.stellar.org`) |
| `TESTNET_RPC_URL`        | `https://soroban-testnet.stellar.org`                |
| `PUBNET_RPC_URL`         | `https://soroban.stellar.org` (or leave as `not-set`) |
| `REDIS_HOST`             | `localhost`                                          |
| `REDIS_PORT`             | `6379`                                               |

**Optional — features degrade gracefully:**

| Variable                | Purpose               | Notes                            |
| ----------------------- | --------------------- | -------------------------------- |
| `SENTRY_KEY`            | Error tracking        | Leave as `not-set` for local dev |
| `BLOCKAID_API_KEY`      | Transaction scanning  | Leave as `not-set` for local dev |
| `COINBASE_API_KEY/SECRET` | Pricing data        | Leave as `not-set` for local dev |
| `HORIZON_*_URL`         | Horizon endpoints     | Defaults work for most dev tasks |

## Key Commands

```bash
make check              # All quality checks (lint, fmt, vet, shadow, etc.)
make unit-test          # Unit tests
make unit-test-coverage # Unit tests with 80% coverage threshold
make integration-test   # Integration tests (uses testcontainers)
make build              # Build binary
make run                # Run the server
make docker-up          # Docker Compose up
```

See `Makefile` for the complete list.

## Code Conventions

- **Formatting:** `gofmt` and `gofumpt`
- **Linting:** `golangci-lint` (5m timeout)
- **Import organization:** `goimports` with local prefix
  `github.com/stellar/freighter-backend-v2`
- **Tests:** All changes must be covered. Coverage threshold: 80%.
- **Documentation:** Doc comments on all exported functions per
  [Effective Go](https://golang.org/doc/effective_go.html#commentary)

## Testing

**Unit tests:**
```bash
make unit-test
```

**Integration tests** (uses `testcontainers-go` for Redis):
```bash
make integration-test
```

## Pull Requests

- Branch from `main`
- Commit messages: action verb in present tense
- Code must pass `make check`
- All tests must pass with 80% coverage
- Follow [Effective Go](https://golang.org/doc/effective_go.html)

**CI runs on every PR:** check + build + unit-tests (80% coverage) +
integration-tests. See `.github/workflows/go.yaml`.

## Related Repositories

- [stellar/freighter-backend](https://github.com/stellar/freighter-backend)
  (TypeScript) — V1 backend for balances, subscriptions, feature flags
- [stellar/freighter](https://github.com/stellar/freighter) — Browser extension
- [stellar/freighter-mobile](https://github.com/stellar/freighter-mobile) — Mobile app

## Security

- **Never log** API keys, auth credentials, or user data
- **Blockaid integration** protects users — don't bypass or weaken
- **Report vulnerabilities** via the
  [Stellar Security Policy](https://github.com/stellar/.github/blob/master/SECURITY.md)
  — not public issues
