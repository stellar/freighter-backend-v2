# JWT Auth Middleware — Design

- **Ticket:** [stellar/freighter-backend-v2#88](https://github.com/stellar/freighter-backend-v2/issues/88)
- **Reference:** [Freighter Unified User Model Storage](https://github.com/stellar/wallet-eng-monorepo/blob/main/design-docs/contact-lists/Freighter%20Unified%20User%20Model%20Storage.md) — user ID derivation + auth flow + JWT claims
- **Date:** 2026-06-22
- **Status:** Approved (design), implementing

## Summary

Add a stateless Ed25519/JWT authentication primitive to `freighter-backend-v2`, modeled on
`wallet-backend`'s `JWTManager` pattern but adapted for a **self-asserted identity** model: the
JWT's `sub` claim *is* the caller's **unified user ID** — a hex-encoded Ed25519 public key derived
from the user's seed, which doubles as the signature-verification key — and the server verifies each
request's signature against it. No registration, no session state, no DB lookup, **no server-side key
or secret to provision**.

This ticket delivers the **verifier + middleware only**. It is feature-agnostic — it does not gate
any existing production endpoint, and any feature that wants authenticated requests builds on it
later. Existing endpoints keep working unchanged.

## Scope

In scope:
- A pure verifier package (`internal/auth`) that, given an `*http.Request`, returns the authenticated
  `userID` or a typed error.
- An HTTP middleware (`internal/api/middleware/auth.go`) with two modes (permissive / required).
- A global config field (`AuthMode`) that selects the mode, defaulting to permissive.
- Auth adoption/rejection metrics.
- A minimal guarded endpoint (`GET /api/v1/auth/whoami`) to exercise the middleware end-to-end and
  give client teams a surface to validate JWT construction against. *(Optional — can be dropped if
  reviewers prefer zero new production routes; acceptance is also covered by middleware tests.)*

Out of scope (future tickets):
- Any feature endpoints/schema that consume the authenticated user ID.
- Gating existing endpoints (`/accounts/balances`, `/protocols`, etc.).
- The final permissive→strict cutover.

## Rollout model (why two modes)

Shipped Freighter clients today send **no** JWT. New client versions will send a JWT on every
request. We cannot break old clients, so gated endpoints go through a transition:

| Mode | No `Authorization` header | Header present, valid | Header present, invalid |
| --- | --- | --- | --- |
| **public** (ungated) | pass | pass | pass |
| **permissive** (transition, default) | pass (anonymous, no `userID`) | pass (+`userID`) | **401** |
| **required** (post-rollout) | **401** | pass (+`userID`) | **401** |

A present-but-invalid token is **always** rejected (401) — only updated clients send tokens, so a bad
token is a real bug or attack, and rejecting it keeps ticket #88's acceptance criteria true in every
mode while giving clean adoption signal during rollout.

All gated endpoints share one mode and flip together (client adoption is per-app-version, not
per-endpoint), so the mode is a single global config value, not a per-route argument. The cutover is
one config change.

## Architecture

```
internal/auth/                     NEW — pure verifier primitive, no HTTP/config/metrics deps
  claims.go      Claims struct + Validate(methodAndPath, body, maxLifetime)
  parser.go      ParseJWT: read sub → hex-decode → ed25519 key → verify sig (EdDSA only) + leeway
  verifier.go    HTTPRequestVerifier interface + VerifyHTTPRequest(req) (userID, error)
  errors.go      ErrNoToken (sentinel), ErrUnauthorized, ExpiredTokenError
  helpers.go     HashBody (SHA-256 hex)
  context.go     ContextWithUserID / UserIDFromContext
  *_test.go      table-driven unit tests + a test-only token signer

internal/api/middleware/auth.go    NEW — Auth(verifier, mode) Middleware; 401 via httperror; injects userID
internal/api/handlers/whoami.go    NEW (optional) — echoes authenticated userID
internal/config/config.go          + AuthMode field (permissive|strict), validated at load
internal/metrics/metrics.go        + auth counter (adoption/rejection signal)
internal/api/serve.go              wire Auth(verifier, cfg.AuthMode) onto the whoami route
```

**Boundaries:** `internal/auth` owns the *mechanism* (is this token cryptographically valid for its
claimed identity?). The middleware owns the *policy* (which mode, what to do per outcome, how to
render 401, what to record). The verifier is HTTP-aware only insofar as it reads an `*http.Request`;
it has no knowledge of config, metrics, or middleware.

### Divergences from wallet-backend (by necessity)

1. `sub` is the **unified user ID** — a **hex-encoded raw Ed25519** public key (32 bytes), not a
   strkey `G...` address — so no `strkey` calls; `hex.DecodeString` → `ed25519.PublicKey`. The user ID
   is derived from the seed via HMAC and is deliberately *not* a valid Stellar address.
2. The verification key is **self-asserted** (it *is* the `sub` / user ID), not compared against a
   configured/allowlisted server key. wallet-backend's `ParseJWT` rejects any `sub != m.PublicKey`;
   that check is removed.
3. Adds **±5s leeway** (`jwt.WithLeeway`) for mobile clock skew, per the design doc.
4. **Verify-only.** No signing/generator code and no key/secret config — clients sign, server verifies.

## Verifier internals

```go
type Claims struct {
    BodyHash      string `json:"bodyHash"`
    MethodAndPath string `json:"methodAndPath"`
    jwtgo.RegisteredClaims // Subject (hex unified user ID = Ed25519 pubkey), Issuer, IssuedAt, ExpiresAt
}
```

Constants: `MaxTokenLifetime = 15s`, `ClockSkewLeeway = 5s`. The verifier imposes no
body-size limit of its own: request bodies are already bounded upstream by the
`BodySizeLimit` middleware (`http.MaxBytesReader`), so it reads them in full rather than
risk silently truncating the bytes it hashes.

`ParseJWT(tokenString, methodAndPath, body)`:
1. `ParseUnverified` to read `claims.Subject`.
2. `claims.Validate(methodAndPath, body, MaxTokenLifetime)`:
   - `exp` and `iat` set; `exp - iat ≤ MaxTokenLifetime`;
   - `methodAndPath` matches `"<METHOD> <RequestURI>"` (binds query string);
   - `bodyHash == HashBody(body)`;
   - `Subject` is valid hex decoding to exactly `ed25519.PublicKeySize` (32) bytes.
3. Decode `Subject` → `ed25519.PublicKey`.
4. `jwtgo.ParseWithClaims(..., keyfunc→pubKey, jwtgo.WithValidMethods([]string{"EdDSA"}), jwtgo.WithLeeway(ClockSkewLeeway))`.
   `WithValidMethods` blocks `alg=none`/HS256 confusion attacks.
5. Map `jwtgo.ErrTokenExpired` → `ExpiredTokenError`.

`VerifyHTTPRequest(req) (userID string, err error)`:
- Missing/`non-Bearer` `Authorization` header → `ErrNoToken` (distinct sentinel, **not** wrapping
  `ErrUnauthorized`) so the middleware can tell "no token" (anonymous-eligible) from "bad token".
- Read the full body, then reset `req.Body` so handlers can read it. Accept a case-insensitive `Bearer` scheme (RFC 6750).
- `methodAndPath = fmt.Sprintf("%s %s", req.Method, req.URL.RequestURI())`.
- On success returns `userID = claims.Subject` (the hex unified user ID). Invalid-token errors wrap `ErrUnauthorized`.

## Middleware, modes, error handling

`auth.Mode` enum: `Permissive`, `Required`. `internal/config` parses the string `AuthMode`
(`permissive`|`strict`, default `permissive`) into it and **fails config load on unknown values**.

```go
func Auth(verifier auth.HTTPRequestVerifier, mode auth.Mode) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userID, err := verifier.VerifyHTTPRequest(r)
            switch {
            case err == nil:
                r = r.WithContext(auth.ContextWithUserID(r.Context(), userID))
            case errors.Is(err, auth.ErrNoToken):
                if mode == auth.Required {
                    /* record rejected; */ httperror.Unauthorized("", nil).Render(w); return
                }
                // permissive: pass through anonymously, no userID
            case errors.Is(err, auth.ErrUnauthorized):
                /* record rejected; */ httperror.Unauthorized("", nil).Render(w); return
            default:
                /* operational error (e.g. body read) */ httperror.InternalServerError(...).Render(w); return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

- 401s use v2's `httperror.Unauthorized`, compatible with the `BufferedResponseWriter` from logging
  middleware (same pattern as `CustomHandler`).
- Auth is applied **per-route** by wrapping the handler at registration, so the global
  `Logging`/`Metrics` middleware (outer) naturally capture auth rejections.

## Observability

- Metric: `freighter_auth_requests_total{result, reason}` counter.
  - `result ∈ {authenticated, anonymous, rejected}` — adoption % during rollout.
  - `reason ∈ {ok, no_token, expired, bad_signature, bad_timing, bad_method_path, bad_body_hash,
    bad_subject, malformed, invalid, internal}` — a bounded set of fixed *categories* (never a
    request value like the path or body hash), so rejection spikes can be triaged by cause
    (client clock skew vs. body-hash bug vs. forged signature) without high label cardinality.
- Logging: each rejection is logged via `logger` at info with `reason`, the failure `detail`, and the
  request method/path — **never** the token or body bytes.

## Testing

- **auth pkg unit tests** (table-driven), using a test-only signer helper that mints tokens with a
  generated ed25519 keypair:
  - `claims.Validate`: expired, future-dated, `exp-iat` too long, mismatched `methodAndPath`,
    mismatched `bodyHash`, non-hex `sub`, wrong-length `sub`.
  - `ParseJWT`: valid; tampered payload; wrong signing key; `alg=none`/HS256 rejected; expired →
    `ExpiredTokenError`; ±5s leeway boundary.
  - `VerifyHTTPRequest`: missing header → `ErrNoToken`; bad Bearer prefix; body-hash binding; query
    string binding; body reset after read.
- **middleware tests**: the full truth table — for each mode × {no header, valid, expired, tampered,
  wrong-key}, assert status (200/401) and presence/absence of `userID` in context.
- Acceptance (#88): valid → 200, expired/tampered/wrong-key → 401, covered by middleware tests in
  required mode (and permissive present-but-invalid → 401).

## Operational notes

- New env/config var: `AUTH_MODE` (default `permissive`).
- New route (if `whoami` kept): `GET /api/v1/auth/whoami`.
- New metric: `freighter_auth_requests_total`.
- These should be reflected in `wallet-eng-runbooks` when the endpoint/metric ship (handled via the
  runbook reconcile step).
