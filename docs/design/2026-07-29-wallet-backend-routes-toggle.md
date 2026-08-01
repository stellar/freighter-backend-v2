# Wallet-Backend Routes Toggle — Design

- **Tickets:** _none filed yet_
- **Last updated:** 2026-07-29
- **Status:** Proposed

## Summary

The two wallet-backend-fronted routes are registered and publicly reachable in every
deployed environment, but return 500 on every valid request because their upstream is
unconfigured outside dev:

```
POST /api/v1/accounts/balances
GET  /api/v1/accounts/{address}/transactions
```

This adds a single config flag — `--wallet-backend-routes-enabled` /
`WALLET_BACKEND_ROUTES_ENABLED`, default `true` — controlling whether those routes are
**registered on the mux at all**. Production sets it `false`, so both paths 404 as
though they never existed. Re-enabling is one env-var change plus a pod restart: **no
image rebuild, no code change, no release**.

The flag controls *reachability only*. It does not fix either endpoint.

## Current state

Measured 2026-07-29 with anonymous requests. Neither the prd nor the stg manifest sets
`AUTH_MODE`, so both run the `--auth-mode` flag default of `permissive`
(`cmd/serve/serve.go:87`) — which is why these anonymous calls reach the handler
instead of 401ing.

| Request | prd | stg |
| --- | --- | --- |
| `POST /accounts/balances?network=PUBLIC` | **500** ~0.3s | **500** ~0.1s |
| `POST /accounts/balances?network=TESTNET` | **500** | **500** |
| `GET /accounts/{addr}/transactions?network=PUBLIC` | **500** ~0.4s | **500** ~0.2s |
| `GET /accounts/{addr}/transactions?network=TESTNET` | **500** ~0.1s | **500** ~0.2s |

Supporting evidence that the handlers are reached and otherwise healthy: an invalid
Stellar address returns 400 with the strkey message, `network=FUTURENET` returns 400
`must be PUBLIC or TESTNET`, and `GET /api/v1/ping` returns 200.

### Root cause

Both service methods fail at their first step, on the same guard
(`internal/services/wallet_backend.go:178-181` and `:320-323`):

```go
client := w.configureNetworkClient(network)
if client == nil {
    return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
}
```

A client is constructed only when **both** the URL and the signing key are non-empty
(`wallet_backend.go:70,81`), and those flags default to `""`
(`cmd/serve/serve.go:138-141`). Neither the prd nor the stg deployment sets any
`WALLET_BACKEND_*` variable — verified against `stellar/kube` `upstream/master` at
`e0ce2144d`:

| Env | `WALLET_BACKEND_*` present | Both routes |
| --- | --- | --- |
| **prd** (`kube001-prd-eks/.../wallet-eng-prd/freighter/freighter-backend-v2.yaml`) | none | 500 |
| **stg** (`kube001-dev-eks/.../wallet-eng-stg/freighter/freighter-backend-v2.yaml`) | none | 500 |
| **dev** (`kube001-dev-eks/.../wallet-eng-dev/freighter/freighter-backend-v2.yaml:130-132`) | pubnet URL + key, testnet URL only | work for `PUBLIC` |
| **local** (`deployments/docker-compose.yml:11-18`) | none | 500 |

Two corroborating signals that this is the nil-client path and not an upstream outage:
the failure is **instant** (~100 ms, no round trip) and **identical on both networks
and both routes**.

Dev has no `WALLET_BACKEND_TESTNET_SIGNING_KEY`, so dev's testnet client is nil too and
`?network=TESTNET` 500s there today. Pre-existing; unrelated to this change.

### Why exactly these two routes

`walletBackendService` is passed to exactly two handlers — `NewAccountBalancesHandler`
and `NewAccountHistoryHandler` (`internal/api/serve.go:219,222`). The interface's third
method, `GetHealth`, is wired to no HTTP handler (the only `GetHealth` call in
`internal/api/` is on `rpcService`, in `rpc_health.go`). So these two are the complete
set of wallet-backend-dependent routes, and gating both closes the whole surface.

## Goals

- Both wallet-backend-fronted routes are unreachable in production.
- They stay reachable everywhere else (dev, stg, local), for development and for
  verifying the eventual fix.
- Re-enabling in production is a config change, not a code change or release.

## Non-goals

- **Fixing either endpoint.** Adding the missing `WALLET_BACKEND_*` variables is
  separate work.
- **Changing any response shape**, error string, or handler.
- **Client changes.** The extension calls neither endpoint: zero references to
  `accounts/balances` across `freighter/@shared` and `freighter/extension/src`.

## Design

Four changes, all in `freighter-backend-v2`.

### 1. Config field

`AppConfig` (`internal/config/config.go`) gains `WalletBackendRoutesEnabled bool`,
documenting both gated routes and the shared failure mode.

### 2. Flag

`cmd/serve/serve.go`:

```go
cmd.Flags().BoolVar(&s.Cfg.AppConfig.WalletBackendRoutesEnabled, "wallet-backend-routes-enabled", true, "...")
```

Default `true` keeps dev, stg, and local behaviour unchanged; prd opts out explicitly.
Viper's `AutomaticEnv` with a `-`→`_` replacer (`internal/utils/cmdutil.go:59-60`) binds
this to `WALLET_BACKEND_ROUTES_ENABLED` with no extra wiring — the same mechanism as the
existing `--db-enabled` / `DB_ENABLED` pair (`cmd/serve/serve.go:118`), which is the
closest precedent in the repo.

### 3. Route table

`route` (`internal/api/serve.go`) gains an `enabled bool` field, set explicitly on all
11 entries, and `initHandlers` skips disabled routes:

```go
for _, rt := range rts {
    if !rt.enabled {
        logger.Warn("route disabled by config; not registering", "method", rt.method, "pattern", rt.pattern)
        continue
    }
    // ... existing gating + registration
}
```

**Why a field rather than conditionally appending to the table.** The table's stated
purpose is to be the one source of truth that both `initHandlers` and the strict-mode
guard test enumerate, so a route "cannot silently skip the auth guard." Making the
table's *shape* depend on config breaks that property and hides disabled routes from the
guard test. A field keeps the table flat and puts each route's state where a reviewer
already looks — the same argument that comment makes for `gated`.

The cost is that the 9 unrelated route lines each gain a `true`, because Go positional
composite literals require every field. That is accepted as a one-time mechanical diff.

**Why one flag for both routes.** They share one dependency and one failure mode, so
there is no state where enabling exactly one is correct. A second flag would add a knob
whose only novel setting is a misconfiguration. If a route is later added that *can*
work without wallet-backend, it should get its own gate rather than widening this one.

### 4. Tests

- `testCfg` (`internal/api/serve_test.go`) **must** set `WalletBackendRoutesEnabled:
  true`. It builds a mostly zero-value `AppConfig`, which would make the flag `false` in
  tests. The existing `TestApiServer_initHandlers_AllUserFacingRoutesGatedInStrict`
  iterates `routes()` and asserts 401 for every gated route; with these routes present in
  the table but unregistered it would get 404, and **that test would fail** in a way that
  reads like an auth regression. That guard's assertion message now names the
  unregistered-route case explicitly.
- Table-driven over both routes: 404 when disabled, and 401-in-strict when enabled
  (proving each is registered *and* still auth-gated — the assertion that stops the flag
  from becoming an accidental auth bypass).
- `WalletBackendRoutesGatedTogether` asserts that `routes()` disables *exactly* these two
  patterns, so a change gating only one — leaving the other publicly 500ing in prd —
  fails.
- Command-level tests cover the config path production actually uses: the flag defaults to
  `true`, and `WALLET_BACKEND_ROUTES_ENABLED=false`/`=true` reach the config field through
  viper. The api-package tests set the field directly, so without these a renamed flag or
  broken env binding would leave them green while both endpoints stayed registered in
  prd — a fail-**open** on the control this flag exists to provide.

All guards were mutation-tested rather than assumed: flipping the default, renaming the
flag to a different word, removing `v.AutomaticEnv()`, and reverting account-history to
always-enabled are each caught by the intended test.

## Behaviour when disabled

Both paths return **404** with `net/http`'s default body. Each pattern is registered
under a single method, so an unregistered one yields 404 rather than 405 — Go's
`ServeMux` returns 405 only when the same pattern exists under a different method.

Nothing downstream of the mux runs: no handler, no auth middleware, no wallet-backend
call.

**Observability consequence:** the metrics middleware labels unmatched requests
`handler="unknown"` (`internal/api/middleware/metrics.go:49`), so while disabled this
traffic is counted as `unknown` / 404 rather than under its own handler labels. Anything
keyed on those labels goes quiet — which is the intent, but it is the first thing to
check when wondering where the series went.

## Per-environment outcome

| Env | Flag | Reachable | Actually work |
| --- | --- | --- | --- |
| **prd** | `WALLET_BACKEND_ROUTES_ENABLED="false"` (new) | **404** | — |
| **stg** | default `true` | reachable | **no** — still 500; `WALLET_BACKEND_*` missing |
| **dev** | default `true` | reachable | yes, for `PUBLIC` |
| **local** | default `true` | reachable | no — compose sets no `WALLET_BACKEND_*` |

Turning the flag on does not make either endpoint work. Only stg and prd's missing
wallet-backend configuration does that.

## Deployment

A companion change in `stellar/kube` adds to
`kube001-prd-eks/namespaces/wallet-eng-prd/freighter/freighter-backend-v2.yaml`:

```yaml
- name: WALLET_BACKEND_ROUTES_ENABLED
  value: "false"
```

**Order matters.** Land the env var **before** bumping the prd image. The variable is
inert on the current image (viper ignores an env var with no matching flag), so setting
it first means there is never a window where the new image runs in prd with the routes
enabled. The reverse order leaves exactly such a window.

Re-enabling later: delete the variable (or set `"true"`) and restart the pod.

`MODE`-based production detection was considered and rejected: `--mode` exists
(`cmd/serve/serve.go:86`, default `"development"`) but is read nowhere for behaviour, and
it is set in **none** of the three v2 manifests — so a `MODE != "production"` default
would have evaluated to enabled in prd and failed open.

## Alternatives considered

**Gate on whether the wallet-backend upstream is configured** — register the routes only
when a client exists for some network, by adding a `HasClient(network string) bool` method
to `types.WalletBackendService`. This needs **no kube change at all** (prd and stg are
unconfigured, so both would 404 immediately) and self-heals when the env vars land.
Rejected as too implicit: the routes would switch on as a side effect of configuring
wallet-backend, and it provides no way to hold them off in an environment where
wallet-backend *is* configured. The explicit flag was preferred for being obvious at the
point of decision.

**Two independent flags, one per route.** Rejected: see "Why one flag for both routes."

**Block the paths at the Traefik edge in `stellar/kube`** — no backend change,
revert-to-re-enable. Rejected because the handlers stay reachable to in-cluster callers
and nothing in the backend repo records that they are off.

**Delete the route lines** — smallest diff, but re-enabling becomes a code change,
review, and release rather than a config flip.

**Return 503 with an explanatory body** instead of 404. Rejected: it advertises endpoints
that are intentionally closed, and it requires handlers where non-registration needs
none. Reconsider if a client ever needs to distinguish "temporarily disabled" from "does
not exist" — nothing does today.

## Known gaps

- **Neither stg nor prd can serve these routes** until `WALLET_BACKEND_PUBNET_URL`,
  `WALLET_BACKEND_PUBNET_SIGNING_KEY`, and their testnet counterparts are configured. The
  signing keys are secrets and must be seeded in each namespace's own Vault folder —
  cross-namespace reads are blocked by the `restrict-external-secrets-to-namespaces`
  Kyverno policy (`kube001-dev-eks/cluster-services/kyverno-dev-02.yaml`, and its prd
  counterpart).
- **Dev's `TESTNET` requests 500** for want of `WALLET_BACKEND_TESTNET_SIGNING_KEY`.
- The v2 runbooks write the balances path as `/api/v1/account-balances`, which does not
  exist (the real route is `/api/v1/accounts/balances`). Tracked separately; note
  `freighter-backend` v1 genuinely serves `/api/v1/account-balances/<pubkey>`, so a
  blanket rename would break the v1 runbooks.

## Follow-ups

- Add `WALLET_BACKEND_ROUTES_ENABLED` to the wallet-eng runbooks (new operational env
  var; reachability of both routes in prd now depends on it).
- Configure wallet-backend for stg, then prd, and remove the variable.
