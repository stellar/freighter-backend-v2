# Balances Endpoint Toggle — Design

- **Tickets:** _none filed yet_
- **Last updated:** 2026-07-29
- **Status:** Proposed

## Summary

`POST /api/v1/accounts/balances` is registered and publicly reachable in every deployed
environment, but returns 500 on every valid request because its wallet-backend upstream is
unconfigured outside dev. This adds a single config flag — `--balances-enabled` /
`BALANCES_ENABLED`, default `true` — that controls whether the route is **registered on the mux at
all**. Production sets it `false`, so the path 404s as though it never existed. Re-enabling is one
env-var change plus a pod restart: **no image rebuild, no code change, no release**.

The flag controls *reachability only*. It does not fix the endpoint.

## Current state

Measured 2026-07-29 with anonymous requests. Neither the prd nor the stg manifest sets `AUTH_MODE`,
so both run the `--auth-mode` flag default of `permissive` (`cmd/serve/serve.go:87`) — which is why
these anonymous calls reach the handler instead of 401ing:

| Request | Result |
| --- | --- |
| `POST /api/v1/accounts/balances?network=PUBLIC` (prd) | **500** `Failed to get account balances`, ~0.3s |
| `POST /api/v1/accounts/balances?network=PUBLIC` (stg) | **500**, ~0.1s |
| `POST /api/v1/accounts/balances?network=TESTNET` (stg) | **500**, ~0.1s |
| Same, invalid Stellar address | 400 with the strkey message — handler *is* reached |
| Same, `network=FUTURENET` | 400 `must be PUBLIC or TESTNET` — as designed |
| `GET /api/v1/ping` | 200 `{"status":"healthy"}` — the pods are fine |

### Root cause

`GetBalancesByAccountAddresses` fails at its first step
(`internal/services/wallet_backend.go:178-181`):

```go
client := w.configureNetworkClient(network)
if client == nil {
    return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
}
```

A client is constructed only when **both** the URL and the signing key are non-empty
(`wallet_backend.go:70,81`), and those flags default to `""` (`cmd/serve/serve.go:138-141`).
Neither the prd nor the stg deployment sets any `WALLET_BACKEND_*` variable — verified against
`stellar/kube` `upstream/master` at `e0ce2144d`:

| Env | `WALLET_BACKEND_*` present | Balances |
| --- | --- | --- |
| **prd** (`kube001-prd-eks/namespaces/wallet-eng-prd/freighter/freighter-backend-v2.yaml`) | none | 500 |
| **stg** (`kube001-dev-eks/namespaces/wallet-eng-stg/freighter/freighter-backend-v2.yaml`) | none | 500 |
| **dev** (`kube001-dev-eks/namespaces/wallet-eng-dev/freighter/freighter-backend-v2.yaml:130-132`) | pubnet URL + key, testnet URL only | works for `PUBLIC` |
| **local** (`deployments/docker-compose.yml:11-18`) | none | 500 |

Two corroborating signals that this is the nil-client path and not an upstream outage: the failure
is **instant** (~100ms, no round trip) and **identical on both networks**.

Dev has no `WALLET_BACKEND_TESTNET_SIGNING_KEY`, so dev's testnet client is nil too and
`?network=TESTNET` 500s there today. Pre-existing; unrelated to this change.

## Goals

- `POST /api/v1/accounts/balances` is unreachable in production.
- It stays reachable everywhere else (dev, stg, local), where it is used for development and for
  verifying the eventual fix.
- Re-enabling in production is a config change, not a code change or release.

## Non-goals

- **Fixing the endpoint.** Adding the missing `WALLET_BACKEND_*` variables is separate work.
- **Gating `GET /api/v1/accounts/{address}/transactions`.** It is broken identically and for the
  same reason — same nil client, same instant 500, also public in prd and stg — but it is
  deliberately left alone here. See [Known gaps](#known-gaps).
- **Changing any response shape**, error string, or the handler itself.
- **Client changes.** The extension makes no calls to this endpoint: zero references to
  `accounts/balances` across `freighter/@shared` and `freighter/extension/src`. Nothing depends on
  it today.

## Design

Four changes, all in `freighter-backend-v2`.

### 1. Config field

`AppConfig` (`internal/config/config.go`) gains:

```go
// BalancesEnabled controls whether POST /api/v1/accounts/balances is registered.
// When false the route is never added to the mux, so the path 404s exactly as an
// unknown path would: no handler runs and no wallet-backend call is attempted.
// Defaults true; production sets it false while the endpoint's wallet-backend
// upstream is unconfigured there.
BalancesEnabled bool
```

### 2. Flag

`cmd/serve/serve.go`:

```go
cmd.Flags().BoolVar(&s.Cfg.AppConfig.BalancesEnabled, "balances-enabled", true,
    "Register POST /api/v1/accounts/balances. Set false (env BALANCES_ENABLED) to leave the route unregistered so the path 404s.")
```

Default `true` keeps dev, stg, and local behaviour unchanged; prd opts out explicitly. Viper's
`AutomaticEnv` with a `-`→`_` replacer (`internal/utils/cmdutil.go:59-60`) binds this to
`BALANCES_ENABLED` with no extra wiring — the same mechanism as the existing `--db-enabled` /
`DB_ENABLED` pair (`cmd/serve/serve.go:118`), which is the closest precedent in the repo.

### 3. Route table

`route` (`internal/api/serve.go:185-190`) gains an `enabled bool` field, set explicitly on all 11
entries, and `initHandlers` skips disabled routes:

```go
for _, rt := range rts {
    if !rt.enabled {
        continue
    }
    // ... existing gating + registration
}
```

**Why a field rather than conditionally appending to the table.** The table's stated purpose
(`serve.go:178-184`) is to be the one source of truth that both `initHandlers` and the strict-mode
guard test enumerate, so a route "cannot silently skip the auth guard." Making the table's *shape*
depend on config breaks that property and hides the disabled route from the guard test. A field
keeps the table flat and puts each route's state where a reviewer already looks — the same argument
that comment makes for `gated`.

The cost is that the 10 unrelated route lines each gain a `true`, because Go positional composite
literals require every field. That is accepted as a one-time mechanical diff.

### 4. Tests

- `testCfg` (`internal/api/serve_test.go:33-40`) **must** set `BalancesEnabled: true`. It currently
  builds a mostly zero-value `AppConfig`, which would make the flag `false` in tests. The existing
  `TestApiServer_initHandlers_AllUserFacingRoutesGatedInStrict` iterates `routes()` and asserts 401
  for every gated route; with balances present in the table but unregistered it would get 404, and
  **that test would fail**. Setting it `true` keeps the guard covering the route.
- New: disabled → `POST /api/v1/accounts/balances` returns **404**.
- New: enabled + `strict` → returns **401** for an anonymous request, proving the route is both
  registered *and* still auth-gated. This is the assertion that stops the flag from becoming an
  accidental auth bypass.

## Behaviour when disabled

The path returns **404** with `net/http`'s default body. Because balances is registered only for
`POST`, an unregistered pattern yields 404 rather than 405 — Go's `ServeMux` returns 405 only when
the same pattern exists under a different method.

Nothing downstream of the mux runs: no handler, no auth middleware, no wallet-backend call.

**Observability consequence:** the metrics middleware labels unmatched requests
`handler="unknown"` (`internal/api/middleware/metrics.go:49`), so while disabled, balances traffic
is counted as `unknown` / 404 rather than under its own handler label. Anything keyed on the
balances handler label goes quiet — which is the intent, but it is the first thing to check when
wondering where the series went.

## Per-environment outcome

| Env | Flag | Reachable | Actually works |
| --- | --- | --- | --- |
| **prd** | `BALANCES_ENABLED="false"` (new) | **404** | — |
| **stg** | default `true` | reachable | **no** — still 500s; `WALLET_BACKEND_*` missing |
| **dev** | default `true` | reachable | yes, for `PUBLIC` |
| **local** | default `true` | reachable | no — compose sets no `WALLET_BACKEND_*` |

Turning the flag on does not make the endpoint work. Only stg and prd's missing wallet-backend
configuration does that.

## Deployment

A companion change in `stellar/kube` adds to
`kube001-prd-eks/namespaces/wallet-eng-prd/freighter/freighter-backend-v2.yaml`:

```yaml
- name: BALANCES_ENABLED
  value: "false"
```

**Order matters.** Land the env var **before** bumping the prd image. The variable is inert on the
current image (viper ignores an env var with no matching flag), so setting it first means there is
never a window where the new image runs in prd with the route enabled. The reverse order leaves
exactly such a window.

Re-enabling later: delete the variable (or set `"true"`) and restart the pod.

`MODE`-based production detection was considered and rejected: `--mode` exists
(`cmd/serve/serve.go:86`, default `"development"`) but is read nowhere for behaviour, and it is set
in **none** of the three v2 manifests — so a `MODE != "production"` default would have evaluated to
enabled in prd and failed open.

## Alternatives considered

**Gate on whether the wallet-backend upstream is configured** — register the route only when a
client exists for some network, by adding a `HasClient(network string) bool` method to
`types.WalletBackendService`. This needs **no kube change at all** (prd and stg are unconfigured, so
both would 404 immediately) and self-heals when the env vars land. Rejected as too implicit: the
endpoint would switch on as a side effect of configuring wallet-backend, which also enables
account-history, and it provides no way to hold balances off in an environment where wallet-backend
*is* configured. The explicit flag was preferred for being obvious at the point of decision.

**Block the path at the Traefik edge in `stellar/kube`** — no backend change, revert-to-re-enable.
Rejected because the handler stays reachable to in-cluster callers and nothing in the backend repo
records that the endpoint is off.

**Delete the route line** — smallest diff, but re-enabling becomes a code change, review, and
release rather than a config flip.

**Return 503 with an explanatory body** instead of 404. Rejected: it advertises an endpoint that is
intentionally closed, and it requires a handler where non-registration needs none. Reconsider if a
client ever needs to distinguish "temporarily disabled" from "does not exist" — nothing does today.

## Known gaps

- **`GET /api/v1/accounts/{address}/transactions` remains broken and public** in prd and stg,
  failing identically (`Failed to get account transactions`, instant 500, same nil client). This
  design deliberately does not gate it. Closing it means either a second flag or widening this one
  — a decision left open.
- **Neither stg nor prd can serve balances** until `WALLET_BACKEND_PUBNET_URL`,
  `WALLET_BACKEND_PUBNET_SIGNING_KEY`, and their testnet counterparts are configured. The signing
  keys are secrets and must be seeded in each namespace's own Vault folder — cross-namespace reads
  are blocked by the `restrict-external-secrets-to-namespaces` Kyverno policy
  (`kube001-dev-eks/cluster-services/kyverno-dev-02.yaml`, and its prd counterpart).
- **Dev's `TESTNET` balances 500** for want of `WALLET_BACKEND_TESTNET_SIGNING_KEY`.

## Follow-ups

- Add `BALANCES_ENABLED` to the wallet-eng runbooks (new operational env var; route reachability in
  prd now depends on it).
- Decide whether account-history gets the same treatment.
