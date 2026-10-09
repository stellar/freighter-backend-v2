# JWT Authentication — Design

- **Tickets:** [#88](https://github.com/stellar/freighter-backend-v2/issues/88) (verifier + middleware), [#114](https://github.com/stellar/freighter-backend-v2/issues/114) (applied to all user-facing routes), [#164](https://github.com/stellar/freighter-backend-v2/issues/164) (`sub` is a source id; lazy user resolution), [#165](https://github.com/stellar/freighter-backend-v2/issues/165) (`POST /api/v1/user/link` with signed consents)
- **Reference:** [Freighter Unified User Model Storage](https://github.com/stellar/wallet-eng-monorepo/blob/main/design-docs/contact-lists/Freighter%20Unified%20User%20Model%20Storage.md) — user ID derivation, auth flow, JWT claims
- **Last updated:** 2026-10-09
- **Status:** Current

## Summary

`freighter-backend-v2` authenticates requests with a stateless Ed25519/JWT primitive using a
**self-asserted identity** model: the JWT's `sub` claim *is* the caller's **source id** — a
hex-encoded Ed25519 public key derived from a seed phrase or an account secret key, which doubles as
the signature-verification key. The server verifies each request's signature against `sub`. There is
no registration, no session state, **no DB lookup in the middleware**, and **no server-side key or
secret to provision**.

A source id is **not** a user id. Under the Unified User Model v2 a *user* is a cluster of sources
(every phrase and account key they have proven possession of, via `POST /api/v1/user/link`), and
`users.canonical_source_id` — the source that created the cluster — is the user id as exposed to
clients. Mapping a source to its user is one primary-key lookup, `users.ResolveUser`, that **only
the handlers that need a user make, lazily**. The middleware attaches the source id and the verified
`iat` to the request context and nothing else, so `/protocols`, `/token-prices` and every other
route that does not need a user never touch the database. See [Source → user
resolution](#source--user-resolution).

Every user-facing `/api/v1` route runs the auth middleware. Infra health probes do not.

## Identity model

- The source id (`sub`) is a **hex-encoded raw Ed25519 public key** (32 bytes), derived from the
  user's seed phrase or account secret key via HMAC. It is deliberately *not* a valid Stellar
  `G...` strkey address.
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

## Source → user resolution

The middleware is pure signature verification and must stay that way: it has no database
dependency, and its cost is one Ed25519 verify regardless of how many user-scoped routes exist. What
it proves is "the caller holds the private key for `sub`", so what it attaches to the context is
exactly that — `auth.ContextWithSourceID` — plus the token's verified `iat`
(`auth.ContextWithIssuedAt`), which the link handler signs consents over and must never read from
the body.

Resolving the source to a user is `users.ResolveUser(ctx, sourceID) (userID uuid.UUID,
canonicalSourceID string, err error)` in `internal/users`, the package that owns the `users` and
`user_sources` tables. It is one primary-key lookup on `user_sources` joined to `users`. A source
with no row returns the `users.ErrSourceNotFound` sentinel, which handlers map to `404`.

**Access rule for every user-scoped handler** (recorded in the `internal/users` package doc; `#90`
contacts is the first consumer):

1. read the source id from the context with `auth.SourceIDFromContext`;
2. call `users.ResolveUser`;
3. scope every query by the returned `user_id`. Never key storage by the source id, and never accept
   a user or source id from the request.

**No cache.** The middleware already does no database work, so what a cache would remove is one
primary-key lookup. Redis is a round trip to another service, so it would save Postgres CPU, not
latency. The mapping is immutable today (nothing moves, retires or deletes a source or user; detach
is deferred), so a cache would need no invalidation — but if one is ever added, it is because
something made the mapping mutable, and that change must define the invalidation.

## Modes and rollout

Shipped Freighter clients today send **no** JWT; newer client versions send a JWT on every request.
To avoid breaking old clients, auth has two modes, selected by one global config value
(`AUTH_MODE` / `--auth-mode`, default `permissive`):

| Mode | No `Authorization` header | Valid | Invalid: wrong clock | Invalid: any other reason |
| --- | --- | --- | --- | --- |
| **permissive** (default) | pass (anonymous, no `sourceID`) | pass (+`sourceID`) | pass (anonymous, no `sourceID`) | **401** |
| **strict** (`auth.Required`) | **401** | pass (+`sourceID`) | **401** | **401** |

"Wrong clock" is exactly two reasons, the two directions a clock can be wrong:

| reason | meaning | signature verified? |
| --- | --- | --- |
| `expired` | clock lagging — `exp` already past | **yes** — raised by `jwtgo.ParseWithClaims`, and jwt/v5 returns on signature failure *before* validating claims |
| `clock_ahead` | clock fast — `iat`/`exp` too far ahead | **no** — raised in `Claims.Validate`, which runs first |

`clock_ahead` was split out of `bad_timing` for this. What remains in `bad_timing` — missing
`exp`/`iat`, `exp` preceding `iat`, over-long lifetimes — is a malformed or abusive token rather
than a wrong clock, and is still rejected. Serving those would make the permitted-skew rate
meaningless in the exact counter that gates the strict flip.

Because `clock_ahead` precedes signature verification, a **forged** token dated into the future is
served too. That is safe rather than merely tolerated: permitting attaches no `sourceID`, so the
request is byte-for-byte equivalent to one carrying no `Authorization` header — which these routes
already serve. An attacker gains nothing they could not have by sending no token at all. The cost is
measurement, not access: read `invalid_permitted{reason="clock_ahead"}` as an upper bound on
fast-clock clients, not an exact count.

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
measurement for no reason.

Both clock directions are permitted. When that decision was made prod had shown only lagging clocks,
and the argument was that five devices is far too small to conclude fast clocks do not occur. The
data has since caught up with the argument: over the 7 days ending 2026-08-03, prd logged **442
`expired` (84.4%), 78 `clock_ahead` (14.9%), and 4 `malformed` (0.8%)** out of 524 rejections — and
`clock_ahead` was the *majority* on the two most recent days (08-02: 38 vs 44; 08-03: 20 vs 8). Had
this shipped permitting only `expired`, ~15% of rejections and rising would still be 401ing.

These are genuine wrong clocks rather than stale tokens replayed from a retry queue, which would look
identical on the wire: within a single burst the offset stays flat to ±0.5s across spans up to 478s,
whereas a replayed token's apparent offset grows 1s per elapsed second. Offsets are also stable per
device across days.

**Strict stays fail-closed for everything, timing included.** It has no anonymous path to fall back
to, so this is a rollout-window mitigation only: skewed clients must be fixed client-side (deriving
a clock offset from the `Date` response header) before the permissive→strict flip, and that flip
should be gated on **both** timing reasons reaching ~0 across both `iss` values — not on the fix
merely having shipped, since client rollout lags by days.

The two reasons carry different evidential weight, and the criterion must not be narrowed to the
stronger one:

- `invalid_permitted{reason="expired"}` is **authentic per-client**. `ExpiredTokenError` carries the
  signature-verified `iss` out of the failure, and the middleware labels from it rather than
  re-parsing the token unverified — so this is the number to use when deciding *which client team*
  still needs to ship the offset fix.
- `invalid_permitted{reason="clock_ahead"}` is an **upper bound, and unattributable**. No signature
  check ran, so the `iss` label is caller-chosen and one request per increment needs no key: a
  future-dated token claiming any `iss` will do. It cannot distinguish five real fast-clock devices
  from one script.

It is tempting to gate only on `expired` for that reason. Don't: `clock_ahead` is 15% of rejections
and rising, so an `expired`-only gate would read "ready to flip" while a growing fast-clock population
is still broken — trading a fail-safe criterion for a fail-dangerous one. Keep both in the gate, and
treat a nonzero `clock_ahead` as a signal to investigate logs and source IPs rather than something to
read off a dashboard. On the rejection log, `iss_verified` distinguishes the two cases directly.

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
- **Always `auth.Required` (`requireAuth: true`, independent of `AUTH_MODE`):** `POST /api/v1/user/link`.
  It is the only write to the identity tables and has no anonymous form, so it is wrapped with its
  own `middleware.Auth(verifier, auth.Required, m)` value even while the global mode is permissive.
  The `RequireAuthRoutesRejectAnonymousInBothModes` guard test probes every such route under both
  modes with no token and with an expired-but-signed token, the two cases the permissive gate would
  have served.
- **Anonymous in every mode (registered bare, never wrapped):** the infra liveness/readiness
  probes `/api/v1/ping`, `/api/v1/db-health`, `/api/v1/rpc-health`. K8s and the docker-compose
  healthcheck cannot present per-request JWTs, and `db-health` is designed never to fail the
  request; gating any of these would 401 probes under `strict` and cause pod churn.

Because auth wraps the handler *inside* the mux, it runs **after** routing — so the global
`Logging`/`Metrics` middleware (which are outer, wrapping the whole mux) capture auth 401s, and the
HTTP metrics `handler` label (from `r.Pattern`) stays correct for authenticated requests.

A user-scoped route that needs a policy *different* from the global mode (always `auth.Required`
regardless of `AUTH_MODE`) declares `requireAuth: true` in the table. `initHandlers` wraps it with a
second `Auth` value pinned to `auth.Required` (same verifier, same metrics) instead of the shared
`authed`. `requireAuth` implies `gated`, so the strict-mode guard still enumerates it.

## Linking sources: `POST /api/v1/user/link`

A valid JWT proves possession of *some* keypair, and anyone can mint one, so the link endpoint
accepts no claimed source or user id. Every source in the body carries a consent signed by that
source's own key over a domain-separated, newline-delimited message that binds it to the caller:

```
freighter-user-link-v2 \n source \n <id> \n <kind> \n <signingSourceId> \n <iat as unix seconds>
```

`signingSourceId` is the JWT `sub`, and `iat` is the token's **verified** issued-at, read from the
request context (`auth.IssuedAtFromContext`), never from the body. The signer binding is what
matters for security: only the holder of `sub` can use a consent. The `iat` binding is a freshness
bound rather than a binding to one token, since any token whose `iat` falls inside the clock-skew
window (about `2 × ClockSkewLeeway + MaxTokenLifetime`) rebuilds the same message; it limits how
long a captured consent stays usable by its own signer and nothing more. `kind` (`phrase` |
`secret_key`) is read from the signed bytes and stored from there, so a kind edited after signing
fails verification. `auth.LinkConsentMessage` builds the bytes and `auth.VerifyLinkConsent` checks
one consent; the format is a cross-platform contract pinned by fixture vectors (#166), so a later
change needs a new domain prefix, not an edit.

Resolution (`users.Linker.Link`) runs in one transaction with `SET LOCAL` lock and statement
timeouts and takes **one** `pg_advisory_xact_lock`, on `hashtext(sub)`, so two callers signing as the
same source serialize. It then resolves `sub` with `ResolveUser` on the transaction (an existing row
is the caller's user; otherwise, under a savepoint, a new `users` row with `canonical_source_id =
sub`, adopting an existing `users` row with that canonical id if its source row was ever lost) and
writes every source that has no row with one sorted multi-row
`INSERT ... ON CONFLICT (source_id) DO NOTHING RETURNING source_id`. An id that comes back unwritten
has a committed row: under the caller it needs nothing, under anyone else it goes in `conflicts`
and is left untouched. If the *signer's* own row comes back unwritten, another caller linked it as
one of their sources in the meantime; the savepoint is rolled back and the signer is re-resolved as
a member of that cluster, exactly as if that request had arrived first. A claimed source is never
moved, two populated clusters never combine, no row is retired, and `canonical_source_id` is never
updated.

This departs from the v2 design's *Resolution* step as written, which takes a per-source advisory
lock on every submitted id. Those locks decided who wins a contested source, which the
`user_sources` primary key already decides; what they added was up to `MaxLinkSources` entries per transaction
in Postgres's shared lock table (`max_locks_per_transaction × max_connections`, about 6400 at
defaults), which a handful of pods under concurrent full-size requests could exhaust and take every
transaction in the database down with them. Sorting the batch is what keeps two concurrent inserts
with overlapping ids from deadlocking.

A request carries at most `users.MaxLinkSources` = 32 sources. #165 specified 128; it was lowered
because every row this endpoint writes is permanent and any self-minted key can write some, so the
cap is what bounds how far one request amplifies into storage and ed25519 work. A real wallet is one
phrase plus a handful of keys; a client with more links in several calls under the same signer.
The only rate limiter is per-IP at the ingress (#104, in `stellar/kube`), keyed on an
X-Forwarded-For value that closure itself calls attacker-influenced, and per-`sub` limiting (#105)
cannot help here since an attacker mints a fresh `sub` per request. A per-route ingress limit on
this path is the real mitigation and is infra work; `freighter_user_link_requests_total` and
`freighter_user_link_sources_total` (see Operational notes) are how an attack is seen meanwhile.

Status codes: `400` for a malformed body (empty, more than `MaxLinkSources` sources, a repeated id, a non-
canonical id, an unknown kind, or a body missing `sub`); `403` for a consent that does not verify;
`401` only as a backstop, since the route is always `auth.Required`; `503` with the database
disabled. A conflict is a `200` with the id listed, not an error.

## Architecture

```
internal/auth/                     pure verifier primitive — no HTTP/config/metrics deps
  claims.go      Claims struct + Validate(methodAndPath, body, maxLifetime)
  parser.go      ParseJWT: read sub → hex-decode → ed25519 key → verify sig (EdDSA only) + leeway
  verifier.go    HTTPRequestVerifier interface + VerifyHTTPRequest(req) (Identity{SourceID, Issuer, IssuedAt}, error)
  mode.go        Mode enum (Permissive|Required) + ParseMode("permissive"|"strict")
  errors.go      ErrNoToken (sentinel), ErrUnauthorized, VerificationError + Reason
  helpers.go     HashBody (SHA-256 hex)
  linkproof.go   LinkConsentMessage / VerifyLinkConsent (the v2 consent contract); source kinds; IsCanonicalSourceID
  context.go     ContextWithSourceID / SourceIDFromContext; ContextWithIssuedAt / IssuedAtFromContext
internal/users/users.go            ResolveUser(ctx, sourceID) → (userID, canonicalSourceID); ErrSourceNotFound; the user-scoped access rule
internal/users/link.go             Linker.Link: the only write to users/user_sources, one transaction under ordered advisory locks
internal/api/middleware/auth.go    Auth(verifier, mode, metrics) Middleware; 401 via httperror; injects sourceID + iat; no DB
internal/api/handlers/whoami.go    echoes the authenticated sourceID (auth smoke-test surface; does not resolve the user)
internal/api/handlers/user_link.go POST /api/v1/user/link: validates the body, verifies every consent, then calls Linker.Link
internal/config/config.go          AuthMode field (permissive|strict), validated at load
internal/metrics/metrics.go        auth counter (adoption/rejection signal)
internal/api/serve.go              routes() table + per-route Auth wrapping in initHandlers; health routes gated=false (bare); requireAuth routes pinned to Required
```

**Boundaries:** `internal/auth` owns the *mechanism* (is this token cryptographically valid for its
claimed identity?). The middleware owns the *policy* (mode, per-outcome behavior, 401 rendering,
metrics). The verifier is HTTP-aware only insofar as it reads an `*http.Request`. `internal/users`
owns the *mapping* from a proven source to a user; neither `internal/auth` nor the middleware
imports it.

## Verifier internals

```go
type Claims struct {
    BodyHash      string `json:"bodyHash"`
    MethodAndPath string `json:"methodAndPath"`
    jwtgo.RegisteredClaims // Subject (hex source id = Ed25519 pubkey), Issuer, IssuedAt, ExpiresAt
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

`VerifyHTTPRequest(req) (Identity, error)`:
- Missing/non-Bearer `Authorization` header → `ErrNoToken` (distinct sentinel, **not** wrapping
  `ErrUnauthorized`) so the middleware can tell "no token" (anonymous-eligible) from "bad token".
  A `Bearer` scheme with an empty credential is a bad token, not "no token".
- Read the full body, then reset `req.Body` so handlers can read it. `Bearer` scheme is
  case-insensitive (RFC 6750).
- `methodAndPath = fmt.Sprintf("%s %s", req.Method, req.URL.RequestURI())`.
- On success returns `Identity{SourceID: claims.Subject, Issuer: claims.Issuer, IssuedAt: claims.IssuedAt}`.
  Invalid-token errors wrap `ErrUnauthorized`.

## Middleware, modes, error handling

`auth.Mode` enum: `Permissive`, `Required`. `internal/config` parses `AuthMode`
(`permissive`|`strict`, default `permissive`) into it and **fails config load on unknown values**.
The resolved mode is stored once on the server (`s.authMode`) and bound into the shared `Auth`
value used to wrap routes.

```go
func Auth(verifier auth.HTTPRequestVerifier, mode auth.Mode, m *metrics.Auth) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            identity, err := verifier.VerifyHTTPRequest(r)
            switch {
            case err == nil:
                metrics.RecordAuth(m, "authenticated", "ok")
                ctx := auth.ContextWithSourceID(r.Context(), identity.SourceID)
                r = r.WithContext(auth.ContextWithIssuedAt(ctx, identity.IssuedAt))
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
    `invalid_permitted` is a wrong-clock token (`expired` or `clock_ahead`) served anonymously under
    permissive — see the mode table above for which of the two implies a verified signature.
    It is separate from both `rejected` (these requests succeed) and `anonymous` (a
    client sending a broken token is a different population from one sending none). **Anything
    watching the invalid-token rate — alerts, strict-flip readiness — must sum it with `rejected`,
    or permitted skew will read as having disappeared rather than as still happening.**
  - `reason ∈ {ok, no_token, expired, clock_ahead, bad_signature, bad_timing, bad_method_path, bad_body_hash,
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
  future-dated, tampered, wrong-key}, assert status (200/401) and presence/absence of `sourceID` (and
  `iat`) in context. The `permissive/wrong-key` and `required/expired` rows are the load-bearing ones: they
  prove the timing fall-through is narrow (a bad signature still 401s) and that strict is unchanged.
- **route wiring tests** (`internal/api`): user-facing routes reject anonymous in strict, reject
  invalid tokens in permissive, and expose `sourceID` on a valid token; health probes stay anonymous
  in every mode; an authenticated request keeps its real route label in `freighter_http_requests_total`.

## Operational notes

- Config var: `AUTH_MODE` / `--auth-mode` (default `permissive`).
- Route: `GET /api/v1/auth/whoami` (auth smoke-test surface).
- Route: `POST /api/v1/user/link` (always `auth.Required`; the only identity write).
- Metrics: `freighter_user_link_requests_total{result}` with `result` in `created` | `resolved` |
  `bad_request` | `bad_consent` | `error`, and `freighter_user_link_sources_total{outcome}` with
  `outcome` in `written` | `conflict`. Alert on the rate of `result="created"`: a real wallet creates
  one user, once, so a sustained creation rate is a sybil storage-exhaustion attack, and
  `outcome="written"` says how many permanent rows it is producing. Both label sets are closed and
  never carry a source id. Reflect in `wallet-eng-runbooks` via the runbook reconcile step.
- **link tests:** consent verifier vectors in `internal/auth` (both kinds, signer-as-subject, kind /
  signer / iat altered after signing, wrong key, non-standard base64); the handler's rejection table
  against a fake store that must never be reached; the resolve-by-signer matrix, canonical-root,
  concurrency (same signer ×100, different signers sharing a key) and no-cross-cluster-writes cases
  against Postgres in `internal/users` (`make integration-test`); and an end-to-end suite in
  `internal/integrationtests` that proves the Required wrap under the container's permissive mode
  and the consent's binding to the token's `iat`.
- **`users` pkg tests:** `ResolveUser` returns the user for a known source (from either its
  canonical or a linked source) and `ErrSourceNotFound` for an unknown one, against a fake row
  (unit) and a migrated Postgres (`make integration-test`).
- Metric: `freighter_auth_requests_total`.
- Under `strict`, all user-facing `/api/v1` routes return 401 without a valid JWT; health probes
  remain anonymous. Reflect endpoint/metric/behavior changes in `wallet-eng-runbooks` via the
  runbook reconcile step.
