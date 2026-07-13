# JWT Authentication — Design

- **Tickets:** [#88](https://github.com/stellar/freighter-backend-v2/issues/88) (verifier + middleware), [#114](https://github.com/stellar/freighter-backend-v2/issues/114) (applied to all user-facing routes)
- **Reference:** [Freighter Unified User Model Storage](https://github.com/stellar/wallet-eng-monorepo/blob/main/design-docs/contact-lists/Freighter%20Unified%20User%20Model%20Storage.md) — user ID derivation, auth flow, JWT claims
- **Last updated:** 2026-07-10
- **Status:** Current

## Summary

`freighter-backend-v2` authenticates requests with a stateless Ed25519/JWT primitive using a
**self-asserted identity** model: the JWT's `sub` claim *is* the caller's **unified user ID** — a
hex-encoded Ed25519 public key derived from the user's seed, which doubles as the
signature-verification key. The server verifies each request's signature against `sub`. There is no
registration, no session state, no DB lookup, and **no server-side key or secret to provision**.

Every user-facing `/api/v1` route runs the auth middleware. Infra health probes do not.

## Identity model

- The unified user ID is a **hex-encoded raw Ed25519 public key** (32 bytes), derived from the
  user's seed via HMAC. It is deliberately *not* a valid Stellar `G...` strkey address.
- The verification key is **self-asserted**: it *is* the `sub` claim. There is no configured or
  allowlisted server key to compare against — validity means "this token is cryptographically
  signed by the private key matching the public key it claims as its identity."
- **Verify-only:** clients sign, the server verifies. No signing/generator code, no key/secret
  config.

Because identity is self-asserted, a valid JWT only proves possession of *some* Ed25519 keypair —
anyone can mint one — so auth (even in `strict`) is **not** an anti-sybil or rate-limiting control.
It raises the bar for casual/anonymous abuse but does not bound how many identities a caller can
present; endpoints that need abuse protection (e.g. anything hitting a metered upstream) still
require their own per-route limits or quotas on top of auth.

## Modes and rollout

Shipped Freighter clients today send **no** JWT; newer client versions send a JWT on every request.
To avoid breaking old clients, auth has two modes, selected by one global config value
(`AUTH_MODE` / `--auth-mode`, default `permissive`):

| Mode | No `Authorization` header | Header present, valid | Header present, invalid |
| --- | --- | --- | --- |
| **permissive** (default) | pass (anonymous, no `userID`) | pass (+`userID`) | **401** |
| **strict** (`auth.Required`) | **401** | pass (+`userID`) | **401** |

A present-but-invalid token is **always** rejected (401) in both modes — only updated clients send
tokens, so a bad token is a real bug or attack, and rejecting it gives clean adoption signal during
rollout.

All user-facing routes share one mode and flip together (client adoption is per-app-version, not
per-endpoint), so the mode is a single global config value. The permissive→strict cutover is one
config change.

## Route coverage

Auth is applied **per route**, driven by a single route table (`routes()` in
`internal/api/serve.go`). Each entry declares a `gated` flag; `initHandlers` iterates the table and
wraps every `gated` route with one shared `middleware.Auth(verifier, s.authMode, metrics)` value
bound to the configured mode. The table is the single source of truth for gating — the strict-mode
guard test enumerates the same `routes()`, so a newly-added route is auto-covered and a route added
`gated: false` is a visible, reviewable decision rather than a silent fail-open.

- **Gated (user-facing):** `/api/v1/protocols`, `/api/v1/collectibles`,
  `/api/v1/ledger-key/accounts`, `/api/v1/feature-flags`, `/api/v1/accounts/balances`,
  `/api/v1/token-prices`, `/api/v1/accounts/{address}/transactions`, `/api/v1/auth/whoami`.
- **Anonymous in every mode (registered bare, never wrapped):** the infra liveness/readiness
  probes `/api/v1/ping`, `/api/v1/db-health`, `/api/v1/rpc-health`. K8s and the docker-compose
  healthcheck cannot present per-request JWTs, and `db-health` is designed never to fail the
  request; gating any of these would 401 probes under `strict` and cause pod churn.

Because auth wraps the handler *inside* the mux, it runs **after** routing — so the global
`Logging`/`Metrics` middleware (which are outer, wrapping the whole mux) capture auth 401s, and the
HTTP metrics `handler` label (from `r.Pattern`) stays correct for authenticated requests.

A future user-scoped route that needs a policy *different* from the global mode (e.g. always
`auth.Required` regardless of `AUTH_MODE`) would wrap explicitly with its own `Auth` value —
e.g. `middleware.Auth(verifier, auth.Required, m)(contactsHandler)` — rather than relying on the
shared `authed` the table applies to every `gated` route.

## Architecture

```
internal/auth/                     pure verifier primitive — no HTTP/config/metrics deps
  claims.go      Claims struct + Validate(methodAndPath, body, maxLifetime)
  parser.go      ParseJWT: read sub → hex-decode → ed25519 key → verify sig (EdDSA only) + leeway
  verifier.go    HTTPRequestVerifier interface + VerifyHTTPRequest(req) (userID, error)
  mode.go        Mode enum (Permissive|Required) + ParseMode("permissive"|"strict")
  errors.go      ErrNoToken (sentinel), ErrUnauthorized, VerificationError + Reason
  helpers.go     HashBody (SHA-256 hex)
  context.go     ContextWithUserID / UserIDFromContext
internal/api/middleware/auth.go    Auth(verifier, mode, metrics) Middleware; 401 via httperror; injects userID
internal/api/handlers/whoami.go    echoes the authenticated userID (auth smoke-test surface)
internal/config/config.go          AuthMode field (permissive|strict), validated at load
internal/metrics/metrics.go        auth counter (adoption/rejection signal)
internal/api/serve.go              routes() table + per-route Auth wrapping in initHandlers; health routes gated=false (bare)
```

**Boundaries:** `internal/auth` owns the *mechanism* (is this token cryptographically valid for its
claimed identity?). The middleware owns the *policy* (mode, per-outcome behavior, 401 rendering,
metrics). The verifier is HTTP-aware only insofar as it reads an `*http.Request`.

## Verifier internals

```go
type Claims struct {
    BodyHash      string `json:"bodyHash"`
    MethodAndPath string `json:"methodAndPath"`
    jwtgo.RegisteredClaims // Subject (hex user ID = Ed25519 pubkey), Issuer, IssuedAt, ExpiresAt
}
```

Constants: `MaxTokenLifetime = 15s`, `ClockSkewLeeway = 5s`. The verifier imposes no body-size
limit of its own — request bodies are bounded upstream by the `BodySizeLimit` middleware
(`http.MaxBytesReader`), so it reads them in full rather than risk truncating the bytes it hashes.

`ParseJWT(tokenString, methodAndPath, body)`:
1. `ParseUnverified` to read `claims.Subject`.
2. `claims.Validate(methodAndPath, body, MaxTokenLifetime)`:
   - `exp` and `iat` set; `exp - iat ≤ MaxTokenLifetime`;
   - `iat` not in the future beyond `ClockSkewLeeway`, and `exp` not beyond
     `now + MaxTokenLifetime + ClockSkewLeeway`;
   - `methodAndPath` matches `"<METHOD> <RequestURI>"` (binds the query string);
   - `bodyHash == HashBody(body)`.
3. Decode `Subject` → `ed25519.PublicKey` (hex decoding to exactly 32 bytes).
4. `jwtgo.ParseWithClaims(..., keyfunc→pubKey, WithValidMethods([]string{"EdDSA"}), WithLeeway(ClockSkewLeeway))`.
   `WithValidMethods` blocks `alg=none`/HS256 confusion attacks.

`VerifyHTTPRequest(req) (userID string, err error)`:
- Missing/non-Bearer `Authorization` header → `ErrNoToken` (distinct sentinel, **not** wrapping
  `ErrUnauthorized`) so the middleware can tell "no token" (anonymous-eligible) from "bad token".
  A `Bearer` scheme with an empty credential is a bad token, not "no token".
- Read the full body, then reset `req.Body` so handlers can read it. `Bearer` scheme is
  case-insensitive (RFC 6750).
- `methodAndPath = fmt.Sprintf("%s %s", req.Method, req.URL.RequestURI())`.
- On success returns `userID = claims.Subject`. Invalid-token errors wrap `ErrUnauthorized`.

## Middleware, modes, error handling

`auth.Mode` enum: `Permissive`, `Required`. `internal/config` parses `AuthMode`
(`permissive`|`strict`, default `permissive`) into it and **fails config load on unknown values**.
The resolved mode is stored once on the server (`s.authMode`) and bound into the shared `Auth`
value used to wrap routes.

```go
func Auth(verifier auth.HTTPRequestVerifier, mode auth.Mode, m *metrics.Auth) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userID, err := verifier.VerifyHTTPRequest(r)
            switch {
            case err == nil:
                metrics.RecordAuth(m, "authenticated", "ok")
                r = r.WithContext(auth.ContextWithUserID(r.Context(), userID))
            case errors.Is(err, auth.ErrNoToken):
                if mode == auth.Required { /* record rejected */ httperror.Unauthorized(...).Render(w); return }
                metrics.RecordAuth(m, "anonymous", "no_token") // permissive: pass through
            case errors.Is(err, auth.ErrUnauthorized):
                /* record rejected + log reason */ httperror.Unauthorized(...).Render(w); return
            case middleware.IsMaxBytesError(err):
                /* record rejected */ httperror.RequestEntityTooLarge(...).Render(w); return
            default:
                /* operational error */ httperror.InternalServerError(...).Render(w); return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

401s use v2's `httperror.Unauthorized`, compatible with the `BufferedResponseWriter` from the
logging middleware.

## Observability

- Metric: `freighter_auth_requests_total{result, reason}` counter.
  - `result ∈ {authenticated, anonymous, rejected}` — adoption % during rollout.
  - `reason ∈ {ok, no_token, expired, bad_signature, bad_timing, bad_method_path, bad_body_hash,
    bad_subject, malformed, invalid, too_large, internal}` — a bounded set of fixed *categories*
    (never a request value like the path or body hash), so rejection spikes can be triaged by cause
    without high label cardinality.
- Logging: each rejection is logged via `logger` at info with `reason`, the failure `detail`, and
  the request method/path — **never** the token or body bytes.

## Testing

- **auth pkg unit tests** (table-driven), using a test-only signer that mints tokens with a
  generated ed25519 keypair: `claims.Validate` (expired, future-dated, over-long lifetime,
  mismatched `methodAndPath`/`bodyHash`, non-hex/wrong-length `sub`); `ParseJWT` (valid, tampered,
  wrong key, `alg=none`/HS256 rejected, expired, leeway boundary); `VerifyHTTPRequest` (missing
  header → `ErrNoToken`, bad Bearer prefix, body-hash binding, query-string binding, body reset).
- **middleware tests:** the full truth table — for each mode × {no header, valid, expired,
  tampered, wrong-key}, assert status (200/401) and presence/absence of `userID` in context.
- **route wiring tests** (`internal/api`): user-facing routes reject anonymous in strict, reject
  invalid tokens in permissive, and expose `userID` on a valid token; health probes stay anonymous
  in every mode; an authenticated request keeps its real route label in `freighter_http_requests_total`.

## Operational notes

- Config var: `AUTH_MODE` / `--auth-mode` (default `permissive`).
- Route: `GET /api/v1/auth/whoami` (auth smoke-test surface).
- Metric: `freighter_auth_requests_total`.
- Under `strict`, all user-facing `/api/v1` routes return 401 without a valid JWT; health probes
  remain anonymous. Reflect endpoint/metric/behavior changes in `wallet-eng-runbooks` via the
  runbook reconcile step.
