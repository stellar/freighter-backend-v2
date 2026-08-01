# Design: Per-route auth wrapping (one switch for user-facing routes; health bare)

- **Ticket:** [stellar/freighter-backend-v2#114](https://github.com/stellar/freighter-backend-v2/issues/114) — Apply auth middleware to all endpoints (permissive global; required on user-scoped)
- **Milestone:** Unified User Model
- **Date:** 2026-07-10
- **Status:** Approved

## Problem

JWT auth is currently applied to a single route. `internal/api/serve.go` wraps only
`GET /api/v1/auth/whoami` with `middleware.Auth(verifier, s.authMode, s.appMetrics.Auth)`;
every other `/api/v1` route is registered bare via `mux.HandleFunc(...)`. As a result:

- Endpoints like `/protocols`, `/collectibles`, `/ledger-key/accounts`, `/feature-flags`,
  `/accounts/balances`, `/token-prices`, and `/accounts/{address}/transactions` never run the
  auth middleware. A valid JWT on those requests is ignored — no user context, no auth metrics.
- The `--auth-mode` flag (`permissive` / `strict`) resolves into `s.authMode` but only affects
  whoami. Today `--auth-mode strict` merely makes `GET /api/v1/auth/whoami` reject anonymous
  requests; it has no effect on any other route.

This blocks the Unified User Model rollout, which needs auth context available on all
user-facing endpoints and a single lever to move them from permissive to strict once all clients
send JWTs — **without** locking out the infra health probes, which cannot present JWTs.

## Goals

- Run the `auth.Auth` middleware on every user-facing `/api/v1` route.
- Valid JWTs populate `auth.UserIDFromContext` and auth metrics on all those routes.
- Anonymous requests continue to succeed on all currently-open endpoints (permissive default).
- `--auth-mode strict` is a single switch: every user-facing `/api/v1` route requires a valid
  JWT, and they all flip together.
- Infra health/liveness probes (`/ping`, `/db-health`, `/rpc-health`) stay anonymous in **every**
  mode, including strict.

## Non-goals

- Contacts / user-scoped routes (they do not exist yet). This design makes them trivial to add
  later (see [Extensibility](#extensibility)) but adds nothing for them now.
- Structured logging / metrics enrichment from #106 (`user_id`, `iss`, `status_code`,
  `latency_ms` per request) and the mutable auth-context holder that requires. See
  [Relationship to #106](#relationship-to-106).
- New config or CLI flags.
- Changes to the verifier, JWT format, `auth.Mode` semantics, or `auth/context.go`.

## Approach

Wrap `Auth` **per route**, not globally. Bind one `Auth` middleware value to `s.authMode` and
apply it to each user-facing route at its registration site; register the health probes bare so
they never see `Auth`. The global middleware chain is unchanged.

### `initHandlers()` (`internal/api/serve.go`)

```go
mux := http.NewServeMux()

// Health/liveness probes: registered bare — never wrapped by Auth, so a strict
// flip can never 401 them. K8s/wget probes cannot present per-request JWTs, and
// db-health is designed never to fail the request (serve.go:189).
mux.HandleFunc("GET /api/v1/ping",       handlers.CustomHandler(healthHandler.CheckHealth))
mux.HandleFunc("GET /api/v1/db-health",  handlers.CustomHandler(dbHealthHandler.CheckDBHealth))
mux.HandleFunc("GET /api/v1/rpc-health", handlers.CustomHandler(rpcHealthHandler.CheckRPCHealth))

// One switch for every user-facing route: the same Auth value, bound to
// s.authMode. Flip the flag and they all move permissive -> strict together.
verifier := auth.NewVerifier()
authed := middleware.Auth(verifier, s.authMode, s.appMetrics.Auth)

mux.Handle("GET /api/v1/protocols",                        authed(handlers.CustomHandler(protocolsHandler.GetProtocols)))
mux.Handle("POST /api/v1/collectibles",                    authed(handlers.CustomHandler(collectiblesHandler.GetCollectibles)))
mux.Handle("POST /api/v1/ledger-key/accounts",             authed(handlers.CustomHandler(ledgerKeyAccountsHandler.GetLedgerKeyAccounts)))
mux.Handle("GET /api/v1/feature-flags",                    authed(handlers.CustomHandler(featureFlagsHandler.GetFeatureFlags)))
mux.Handle("POST /api/v1/accounts/balances",               authed(handlers.CustomHandler(accountBalancesHandler.GetAccountBalances)))
mux.Handle("POST /api/v1/token-prices",                    authed(handlers.CustomHandler(tokenPricesHandler.GetPrices)))
mux.Handle("GET /api/v1/accounts/{address}/transactions",  authed(handlers.CustomHandler(accountHistoryHandler.GetAccountTransactions)))
mux.Handle("GET /api/v1/auth/whoami",                      authed(handlers.CustomHandler(whoamiHandler.Whoami)))
```

Only two mechanical changes versus today: the seven currently-bare user-facing routes move from
`mux.HandleFunc(...)` to `mux.Handle(..., authed(...))`, and whoami's inline
`middleware.Auth(verifier, s.authMode, ...)` wrap is replaced by the shared `authed` value
(same behavior). The three health routes stay exactly as they are.

### `initMiddleware()` — unchanged

The common chain stays as it is today; `Auth` is **not** added to it:

```
Recover, ResponseHeader, BodySizeLimit, Logging, Metrics   (outer -> inner)
```

### Why per-route is correct and clean

- **Health exemption is just "don't wrap it."** No second mux, no path allowlist, no catch-all.
- **`r.Pattern` stays correct with zero extra machinery.** `Auth` runs *inside* the mux — after
  routing — so `http.ServeMux` has already set `r.Pattern` before `Auth` touches the request.
  `Metrics` (outside the mux) reads the correct `handler` label whether or not `Auth` forks the
  request via `WithContext`. No mutable holder, no `AuthContext` middleware, no change to
  `auth/context.go`. (Hoisting `Auth` above the mux is what would have broken this; we don't.)
- **Auth posture is legible at each registration site** — you can read a route and know whether
  it is authed, rather than cross-referencing a global chain against an exemption list.
- **No cost for anonymous POSTs.** The verifier returns `ErrNoToken` before reading the body when
  no `Authorization: Bearer` header is present.

## Behavior after the change

| Route class | Mode | Anonymous | Valid JWT | Present-but-invalid token |
| --- | --- | --- | --- | --- |
| Health probes (`/ping`, `/db-health`, `/rpc-health`) | any | success | success | success (never auth-checked) |
| User-facing `/api/v1/*` | `permissive` (default) | success | context + auth metrics populated | 401 |
| User-facing `/api/v1/*` | `strict` | 401 | context + auth metrics populated | 401 |

- **Unknown paths** 404 as today (no wrapped handler, no catch-all) — in both modes.
- **`/metrics`** is unaffected — separate mux and server, its own `Chain` without Auth.

## Extensibility

Per-route wrapping makes the auth posture a property of each registration. A future user-scoped
route declares its own mode with no new machinery:

```go
mux.Handle("PUT /api/v1/contacts", middleware.Auth(verifier, auth.Required, s.appMetrics.Auth)(contactsHandler))
```

`Required` while every other route stays `permissive` — the "required on user-scoped" half of the
ticket, available for free when contacts land.

## Deviation from the ticket body

Ticket #114's body lists `/rpc-health` among endpoints to gate. This design deliberately exempts
it alongside `/ping` and `/db-health`: it is a health/diagnostic surface that may be (or become)
a probe, and pod churn from a strict flip is a worse failure than an anonymous RPC-connectivity
check. Recorded here so the PR reconciles the deviation with the ticket. **Follow-up:** confirm
in `stellar/kube` whether anything probes `/rpc-health`; if not, gating it later is a one-line
change (wrap it with `authed`).

## Relationship to #106

#106 (auth + contacts instrumentation) needs per-request `user_id`/`iss` in the access log. The
outer `Logging` middleware cannot see context that the inner per-route `Auth` attaches (the
`WithContext` fork only propagates to the handler, not back up), so #106 introduces a mutable
auth-context holder seeded above `Logging` and written by `Auth`. That work belongs entirely to
#106; #114 does not need it, because #114 has no requirement to read auth state from an outer
middleware — only the handler (whoami) reads it, and it is downstream of `Auth`.

## Testing

- **Per-route coverage** (`internal/api`, assembled server): for a representative GET
  (`/protocols`) and POST (`/token-prices`) — permissive allows anonymous, rejects a
  present-but-invalid token (401), and a valid token makes the user ID visible to the handler via
  context; strict returns 401 for anonymous.
- **Health probes anonymous in strict** (`s.authMode = strict`): `GET /api/v1/ping`,
  `/db-health`, `/rpc-health` return their normal responses with no `Authorization` header and no
  auth metric recorded.
- **Metrics label** (regression guard): an authenticated request to `/protocols` is recorded in
  `freighter_http_requests_total` with `handler="GET /api/v1/protocols"`, not `"unknown"` —
  confirming per-route `Auth` does not disturb `r.Pattern`.
- **whoami:** anonymous whoami (permissive) → `{"authenticated": false}`; valid token →
  `authenticated: true` + user ID (behavior identical to today).
- **Existing tests:** `internal/api/middleware/auth_test.go` and `cmd/serve/serve_test.go`
  auth-mode tests continue to pass unchanged (no `auth/context.go` change).

## Risks and mitigations

- **A user-facing route added later without `authed(...)` would silently be unauthenticated.**
  Mitigation: the routes are co-located in `initHandlers`, and the per-route coverage test can
  assert the expected set of authed routes. (A registration helper enforcing this is possible but
  not required now — YAGNI.)
- **Flipping `strict` locks out anonymous clients on user-facing routes.** Intended end state;
  flipped only once all clients send JWTs. Default remains `permissive`. Health probes are exempt,
  so a premature flip cannot cause pod churn.

## Operational surface (runbook reconciliation)

No new HTTP routes, Redis keys, metric names, deployment/namespace names, env vars, or hostnames
are introduced. Observable changes: in `strict` mode all user-facing `/api/v1` routes (not just
whoami) return 401 without a valid JWT; health probes remain anonymous in all modes; auth metrics
are now emitted for all user-facing routes. Run runbook reconciliation at end of implementation
per the wallet-eng backend runbook rule.
