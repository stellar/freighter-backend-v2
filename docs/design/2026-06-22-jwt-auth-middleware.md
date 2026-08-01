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

| Mode | No `Authorization` header | Header present, valid | Invalid: `expired` | Invalid: any other reason |
| --- | --- | --- | --- | --- |
| **permissive** (default) | pass (anonymous, no `userID`) | pass (+`userID`) | pass (anonymous, no `userID`) | **401** |
| **strict** (`auth.Required`) | **401** | pass (+`userID`) | **401** | **401** |

The permitted column is exactly `reason="expired"` — no other reason, timing-related or not.

That narrowness is load-bearing rather than incidental. `expired` originates from
`jwtgo.ParseWithClaims`, and jwt/v5 returns on signature failure *before* it validates claims, so an
`expired` classification implies the signature verified: a genuine holder of `sub`'s private key
whose clock lags. Every other reason — including **`bad_timing`, which is also a clock symptom** —
comes out of `Claims.Validate`, which runs *before* signature verification (`auth/parser.go`). A
forged token dated into the future is classified `bad_timing` and never reaches the signature check
at all, so permitting on that reason would carry no proof of who signed it. `bad_timing` is also not
purely a clock signal: it covers missing `exp`/`iat`, `exp` preceding `iat`, and over-long
lifetimes.

Permitting `bad_timing` would not be a privilege escalation — no `userID` is attached either way, so
the request is equivalent to one with no `Authorization` header — but it would falsify the "a bad
signature always stays loud" contract above and let forged tokens inflate the `invalid_permitted`
counter that gates the permissive→strict flip.

**Known gap:** a clock running *fast* by more than the leeway is `bad_timing`, so it still 401s.
Accepted deliberately — in the first 24h of real JWT traffic `bad_timing` was 0 against 335
`expired`. Closing it safely would need a signature-verified, skew-specific reason, i.e. reordering
`parseJWT` to verify before validating claims, which the data does not justify.

This table originally rejected *every* present-but-invalid token in both modes, on the reasoning
that "only updated clients send tokens, so a bad token is a real bug or attack." That reasoning
enumerated two populations and missed a third: **a correct, up-to-date client running on a machine
whose clock is wrong.** In the first 24h of real JWT traffic that third population was 100% of all
rejections (335, from ~5 devices with fixed offsets of 3m04s, 11m05s, 15m14s, 3h01m20s, and exactly
8h00m00s) — see [#147](https://github.com/stellar/freighter-backend-v2/issues/147). Those users were
locked out of every gated route while sending cryptographically valid tokens, on endpoints that
serve anonymous traffic freely. Rejecting them refused a request that would have succeeded had the
client sent no token at all, which defeats the purpose of permissive mode.

Note that rejecting was never what produced the adoption signal — `RecordAuth` fires before the
render decision, so the metric and log are identical either way. Enforcement was coupled to
measurement for no reason. Permitted timing failures are recorded under a distinct `result` label
(`invalid_permitted`) precisely so that signal stays readable.

**Strict stays fail-closed for everything, timing included.** It has no anonymous path to fall back
to, so this is a rollout-window mitigation only: skewed clients must be fixed client-side (deriving
a clock offset from the `Date` response header) before the permissive→strict flip, and that flip
should be gated on the timing reasons reaching ~0 across both `iss` values — not on the fix merely
having shipped, since client rollout lags by days.

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

Constants: `MaxTokenLifetime = 15s`. `ClockSkewLeeway` is the **default** clock-skew tolerance
(`2m`), configurable per deployment via `--auth-clock-skew-leeway` / env `AUTH_CLOCK_SKEW_LEEWAY`
and threaded into the verifier (`NewVerifier(leeway)`); widening it only widens which `iat`/`exp`
values pass the timing gates before signature verification, never the signature check itself. The verifier imposes no body-size
limit of its own — request bodies are bounded upstream by the `BodySizeLimit` middleware
(`http.MaxBytesReader`), so it reads them in full rather than risk truncating the bytes it hashes.

`parseJWT(tokenString, methodAndPath, body, leeway)` (the verifier passes its configured
`leeway`; the exported `ParseJWT(tokenString, methodAndPath, body)` wraps it with the default):
1. `ParseUnverified` to read `claims.Subject`.
2. `claims.Validate(methodAndPath, body, MaxTokenLifetime, leeway)`:
   - `exp` and `iat` set; `exp - iat ≤ MaxTokenLifetime`;
   - `iat` not in the future beyond `leeway`, and `exp` not beyond
     `now + MaxTokenLifetime + leeway`;
   - `methodAndPath` matches `"<METHOD> <RequestURI>"` (binds the query string);
   - `bodyHash == HashBody(body)`.
3. Decode `Subject` → `ed25519.PublicKey` (hex decoding to exactly 32 bytes).
4. `jwtgo.ParseWithClaims(..., keyfunc→pubKey, WithValidMethods([]string{"EdDSA"}), WithLeeway(leeway))`.
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
  - `result ∈ {authenticated, anonymous, rejected, invalid_permitted}` — adoption % during rollout.
    `invalid_permitted` is an **`expired`** token served anonymously under permissive (see the mode
    table above) — and only that reason, so every request counted is signature-verified.
    It is separate from both `rejected` (these requests succeed) and `anonymous` (a
    client sending a broken token is a different population from one sending none). **Anything
    watching the invalid-token rate — alerts, strict-flip readiness — must sum it with `rejected`,
    or permitted skew will read as having disappeared rather than as still happening.**
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
  future-dated, tampered, wrong-key}, assert status (200/401) and presence/absence of `userID` in
  context. The `permissive/wrong-key` and `required/expired` rows are the load-bearing ones: they
  prove the timing fall-through is narrow (a bad signature still 401s) and that strict is unchanged.
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
