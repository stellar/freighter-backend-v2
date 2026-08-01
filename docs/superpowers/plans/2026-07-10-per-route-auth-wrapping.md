# Per-Route Auth Wrapping (#114) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the JWT auth middleware on every user-facing `/api/v1` route (permissive by default, strict via one flag), while keeping the health probes anonymous in every mode.

**Architecture:** Bind one `middleware.Auth` value to `s.authMode` and wrap each user-facing route with it at its registration site in `initHandlers`; leave `/ping`, `/db-health`, `/rpc-health` registered bare. Auth runs *inside* the mux (after routing), so no change to the global middleware chain or to `auth/context.go` is needed, and HTTP metrics keep labeling requests by their matched route.

**Tech Stack:** Go, `net/http.ServeMux` (Go 1.22 method+path patterns), `github.com/prometheus/client_golang`, `github.com/golang-jwt/jwt/v5`, `github.com/stellar/go-stellar-sdk` (txnbuild/xdr), testify.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-10-global-auth-middleware-design.md`.
- Do **not** modify `internal/auth/context.go`, the `middleware.Auth` implementation, the verifier, or the global chain in `initMiddleware`. This ticket only changes route registration + tests.
- Do **not** add the `AuthContext` / mutable-holder machinery — that is #106.
- Health routes that stay bare (anonymous in every mode): exactly `GET /api/v1/ping`, `GET /api/v1/db-health`, `GET /api/v1/rpc-health`.
- User-facing routes that get wrapped with the shared `authed` value: `GET /api/v1/protocols`, `POST /api/v1/collectibles`, `POST /api/v1/ledger-key/accounts`, `GET /api/v1/feature-flags`, `POST /api/v1/accounts/balances`, `POST /api/v1/token-prices`, `GET /api/v1/accounts/{address}/transactions`, `GET /api/v1/auth/whoami`.
- Auth mode strings: `"permissive"` (default) and `"strict"` — parsed by `auth.ParseMode`.
- Run all tests from the repo root: `go test ./internal/api/...`.

---

## File Structure

- **Modify:** `internal/api/serve.go` — `initHandlers()` route registration only (function signature `func (s *ApiServer) initHandlers() (*http.ServeMux, error)` is unchanged). `internal/api/middleware` and `internal/auth` are already imported.
- **Modify (tests):** `internal/api/serve_test.go` — add behavior, health-exemption, valid-token, and metrics-label tests. Uses the existing `newTestAPIServer` and `mustParseURL` helpers already in this file.

No new non-test files. No new dependencies.

---

### Task 1: Wrap user-facing routes with the shared auth middleware

**Files:**
- Modify: `internal/api/serve.go` (`initHandlers`, currently lines 178–236)
- Test: `internal/api/serve_test.go`

**Interfaces:**
- Consumes: `middleware.Auth(verifier auth.HTTPRequestVerifier, mode auth.Mode, m *metrics.Auth) middleware.Middleware`; `auth.NewVerifier() *auth.Verifier`; existing handler constructors in `internal/api/handlers`.
- Produces: `initHandlers()` returns an `*http.ServeMux` where every user-facing route is wrapped with a single `authed` value bound to `s.authMode`, and the three health routes are bare.

- [ ] **Step 1: Write the failing test**

Add to `internal/api/serve_test.go`:

```go
func TestApiServer_initHandlers_UserFacingRoutesRespectAuthMode(t *testing.T) {
	cfgWith := func(authMode string) *config.Config {
		return &config.Config{AppConfig: config.AppConfig{
			ProtocolsConfigPath:        "testdata/protocols.json",
			AccountHistoryDefaultLimit: 20,
			AccountHistoryMaxLimit:     100,
			AuthMode:                   authMode,
		}}
	}

	// feature-flags is a dependency-free user-facing GET that returns 200 with no
	// file or service, so it isolates auth behavior from handler dependencies.

	// Permissive: an anonymous request to a user-facing route passes through.
	mux, err := newTestAPIServer(t, cfgWith("permissive")).initHandlers()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "permissive: anonymous must pass")

	// Permissive: a present-but-invalid bearer token is rejected on that route.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "permissive: invalid token must 401")

	// Strict: an anonymous request to a previously-open route is rejected.
	mux, err = newTestAPIServer(t, cfgWith("strict")).initHandlers()
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "strict: anonymous must 401")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestApiServer_initHandlers_UserFacingRoutesRespectAuthMode -v`
Expected: FAIL — `/api/v1/feature-flags` is currently unwrapped, so the invalid-token case returns 200 (not 401) and the strict-anonymous case returns 200 (not 401).

- [ ] **Step 3: Implement the route-registration change**

In `internal/api/serve.go`, replace the body of `initHandlers` (the block that today registers routes, lines 178–236) with the following. Keep the three health handlers bare; move the `verifier` up and introduce `authed`; switch every user-facing route from `mux.HandleFunc(...)` to `mux.Handle(..., authed(...))`:

```go
func (s *ApiServer) initHandlers() (*http.ServeMux, error) {
	mux := http.NewServeMux()

	// Health/liveness/readiness probes: registered BARE — never wrapped by Auth.
	// K8s and the docker-compose wget healthcheck cannot present per-request JWTs,
	// and db-health is designed never to fail the request; gating any of these
	// would 401 probes under `--auth-mode strict` and cause pod churn.
	healthHandler := handlers.NewHealthHandler()
	mux.HandleFunc("GET /api/v1/ping", handlers.CustomHandler(healthHandler.CheckHealth))

	rpcHealthHandler := handlers.NewRPCHealthHandler(s.rpcService)
	mux.HandleFunc("GET /api/v1/rpc-health", handlers.CustomHandler(rpcHealthHandler.CheckRPCHealth))

	// Pass a true nil interface when the DB is disabled — a nil *pgxpool.Pool
	// boxed in the interface is not == nil, which would defeat the handler's
	// disabled check.
	var dbPinger handlers.DBPinger
	if s.dbPool != nil {
		dbPinger = s.dbPool
	}
	dbHealthHandler := handlers.NewDBHealthHandler(dbPinger)
	mux.HandleFunc("GET /api/v1/db-health", handlers.CustomHandler(dbHealthHandler.CheckDBHealth))

	// Every user-facing route runs the auth middleware. `authed` binds one Auth
	// instance to s.authMode (resolved once in Start); flipping --auth-mode
	// permissive<->strict moves all of these together. A future user-scoped route
	// can opt into auth.Required independently by wrapping with its own Auth value.
	verifier := auth.NewVerifier()
	authed := middleware.Auth(verifier, s.authMode, s.appMetrics.Auth)

	protocolsHandler := handlers.NewProtocolsHandler(s.cfg.AppConfig.ProtocolsConfigPath)
	mux.Handle("GET /api/v1/protocols", authed(handlers.CustomHandler(protocolsHandler.GetProtocols)))

	collectiblesHandler := handlers.NewCollectiblesHandler(s.rpcService, s.cfg.AppConfig.MeridianPayTreasureHuntAddress, s.cfg.AppConfig.MeridianPayTreasurePoapAddress, s.cfg.AppConfig.MeridianPayStellarHouseAddress, s.cfg.RpcConfig.MaxConcurrentRPCCalls)
	mux.Handle("POST /api/v1/collectibles", authed(handlers.CustomHandler(collectiblesHandler.GetCollectibles)))

	ledgerKeyAccountsHandler := handlers.NewLedgerKeyAccountHandler(s.rpcService, s.cfg.AppConfig.MaxLedgerKeyAddresses)
	mux.Handle("POST /api/v1/ledger-key/accounts", authed(handlers.CustomHandler(ledgerKeyAccountsHandler.GetLedgerKeyAccounts)))

	featureFlagsHandler := handlers.NewFeatureFlagsHandler()
	mux.Handle("GET /api/v1/feature-flags", authed(handlers.CustomHandler(featureFlagsHandler.GetFeatureFlags)))

	accountBalancesHandler := handlers.NewAccountBalancesHandler(s.walletBackendService, s.cfg.AppConfig.MaxBalanceAddresses)
	mux.Handle("POST /api/v1/accounts/balances", authed(handlers.CustomHandler(accountBalancesHandler.GetAccountBalances)))

	tokenPricesHandler := handlers.NewTokenPricesHandler(s.pricesService, s.cfg.PricesConfig.MaxTokensPerRequest)
	mux.Handle("POST /api/v1/token-prices", authed(handlers.CustomHandler(tokenPricesHandler.GetPrices)))

	accountHistoryHandler, err := handlers.NewAccountHistoryHandler(
		s.walletBackendService,
		s.cfg.AppConfig.AccountHistoryDefaultLimit,
		s.cfg.AppConfig.AccountHistoryMaxLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("init account-history handler: %w", err)
	}
	mux.Handle("GET /api/v1/accounts/{address}/transactions", authed(handlers.CustomHandler(accountHistoryHandler.GetAccountTransactions)))

	// whoami is a normal user-facing route now; it reads the user ID from context
	// and reports authenticated:false when absent (permissive anonymous).
	whoamiHandler := handlers.NewWhoamiHandler()
	mux.Handle("GET /api/v1/auth/whoami", authed(handlers.CustomHandler(whoamiHandler.Whoami)))

	return mux, nil
}
```

- [ ] **Step 4: Run the new test and the existing whoami test to verify they pass**

Run: `go test ./internal/api/ -run 'TestApiServer_initHandlers_UserFacingRoutesRespectAuthMode|TestApiServer_initHandlers_WhoamiRouteRespectsAuthMode' -v`
Expected: PASS for both. (The existing whoami test still passes because whoami is now wrapped with the same `s.authMode`-bound `authed` value.)

- [ ] **Step 5: Run the full api package tests**

Run: `go test ./internal/api/...`
Expected: PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/api/serve.go internal/api/serve_test.go
git commit -m "feat(auth): apply auth middleware to all user-facing routes (#114)"
```

---

### Task 2: Guard that health probes stay anonymous in strict mode

**Files:**
- Test: `internal/api/serve_test.go`

**Interfaces:**
- Consumes: `newTestAPIServer`; `services.NewRPCService`; sets `s.rpcService` before `initHandlers`.
- Produces: a regression guard asserting `/ping`, `/db-health`, `/rpc-health` never return 401, even in strict mode.

Note: `/rpc-health` invokes `s.rpcService.GetHealth`, so it needs a non-nil service (a nil service panics). Use a tiny in-test stub so the assertion is deterministic and does no network I/O. `/ping` and `/db-health` are dependency-safe (`db-health` returns 200 "disabled" with a nil pool).

- [ ] **Step 1: Write the failing test (and the stub it needs)**

Add to `internal/api/serve_test.go`. First the stub (implements `types.RPCService`; only `GetHealth` is exercised, the rest return zero values):

```go
// stubRPCService is a no-network RPCService double so /rpc-health can be invoked
// in tests without a live RPC endpoint. Only GetHealth is used by CheckRPCHealth.
type stubRPCService struct{}

func (stubRPCService) Name() string { return "stub-rpc" }

func (stubRPCService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

func (stubRPCService) SimulateTx(ctx context.Context, tx *txnbuild.Transaction, network string) (types.SimulateTransactionResponse, error) {
	return types.SimulateTransactionResponse{}, nil
}

func (stubRPCService) SimulateInvocation(ctx context.Context, contractId xdr.ScAddress, sourceAccount *txnbuild.SimpleAccount, functionName xdr.ScSymbol, params []xdr.ScVal, timeout txnbuild.TimeBounds, network string) (types.SimulateTransactionResponse, error) {
	return types.SimulateTransactionResponse{}, nil
}

func (stubRPCService) GetLedgerEntries(ctx context.Context, keys []string, network string) ([]types.LedgerEntryMap, error) {
	return nil, nil
}

func TestApiServer_initHandlers_HealthRoutesAnonymousInStrict(t *testing.T) {
	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "strict",
	}}
	s := newTestAPIServer(t, cfg)
	s.rpcService = stubRPCService{} // so /rpc-health can be invoked without a live RPC

	mux, err := s.initHandlers()
	require.NoError(t, err)

	for _, path := range []string{"/api/v1/ping", "/api/v1/db-health", "/api/v1/rpc-health"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
			"%s must stay anonymous (no 401) even in strict mode", path)
		assert.Equal(t, http.StatusOK, rec.Code, "%s should return 200", path)
	}
}
```

Add these imports to the `import` block of `internal/api/serve_test.go`:

```go
"context"

"github.com/stellar/go-stellar-sdk/txnbuild"
"github.com/stellar/go-stellar-sdk/xdr"

"github.com/stellar/freighter-backend-v2/internal/types"
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/api/ -run TestApiServer_initHandlers_HealthRoutesAnonymousInStrict -v`
Expected: PASS. This is a guard — health routes are bare by construction after Task 1, so it should pass immediately. If it FAILS with 401, a health route was mistakenly wrapped with `authed`; remove the wrap.

- [ ] **Step 3: Run the full api package tests**

Run: `go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/serve_test.go
git commit -m "test(auth): guard health probes stay anonymous under strict (#114)"
```

---

### Task 3: Valid-token end-to-end + HTTP-metrics label regression guard

**Files:**
- Test: `internal/api/serve_test.go`

**Interfaces:**
- Consumes: `s.initHandlers()`, `s.initMiddleware(mux)`, `s.appMetrics.HTTP.RequestsTotal`; `auth.Claims`, `auth.HashBody`, `auth.MaxTokenLifetime`; `github.com/prometheus/client_golang/prometheus/testutil`.
- Produces: proof that (a) a valid JWT reaches the handler with the user ID populated, and (b) an authenticated request is metered with its real route label (not `"unknown"`), confirming per-route Auth does not disturb `r.Pattern`.

- [ ] **Step 1: Write the token-minting helper and the tests**

Add to `internal/api/serve_test.go`. First a local token minter (mirrors the proven one in `internal/api/middleware/auth_test.go`; kept local because that one is package-private):

```go
func mintAPIToken(t *testing.T, priv ed25519.PrivateKey, sub, methodAndPath string) string {
	t.Helper()
	now := time.Now()
	c := auth.Claims{
		BodyHash:      auth.HashBody(nil), // GET requests have no body
		MethodAndPath: methodAndPath,
		RegisteredClaims: jwtgo.RegisteredClaims{
			Subject:   sub,
			Issuer:    "freighter-extension",
			IssuedAt:  jwtgo.NewNumericDate(now),
			ExpiresAt: jwtgo.NewNumericDate(now.Add(auth.MaxTokenLifetime)),
		},
	}
	s, err := jwtgo.NewWithClaims(jwtgo.SigningMethodEdDSA, c).SignedString(priv)
	require.NoError(t, err)
	return s
}

func TestApiServer_initHandlers_ValidTokenPopulatesWhoami(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "permissive",
	}}
	mux, err := newTestAPIServer(t, cfg).initHandlers()
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+mintAPIToken(t, priv, sub, "GET /api/v1/auth/whoami"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "\"authenticated\":true")
	assert.Contains(t, rec.Body.String(), sub)
}

func TestApiServer_authenticatedRequestKeepsRouteMetricLabel(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sub := hex.EncodeToString(pub)

	cfg := &config.Config{AppConfig: config.AppConfig{
		ProtocolsConfigPath:        "testdata/protocols.json",
		AccountHistoryDefaultLimit: 20,
		AccountHistoryMaxLimit:     100,
		AuthMode:                   "permissive",
	}}
	s := newTestAPIServer(t, cfg)
	mux, err := s.initHandlers()
	require.NoError(t, err)
	assembled := s.initMiddleware(mux) // full chain incl. Metrics, so r.Pattern is exercised

	req := httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil)
	req.Header.Set("Authorization", "Bearer "+mintAPIToken(t, priv, sub, "GET /api/v1/feature-flags"))
	rec := httptest.NewRecorder()
	assembled.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// The authenticated request must be metered under its real route pattern,
	// not "unknown" (which is what a pre-routing context fork would have caused).
	got := testutil.ToFloat64(s.appMetrics.HTTP.RequestsTotal.WithLabelValues("GET /api/v1/feature-flags", "GET", "200"))
	assert.Equal(t, float64(1), got, "authed request should be counted under its route pattern")
	unknown := testutil.ToFloat64(s.appMetrics.HTTP.RequestsTotal.WithLabelValues("unknown", "GET", "200"))
	assert.Equal(t, float64(0), unknown, "authed request must not collapse to the unknown handler label")
}
```

Add these imports to `internal/api/serve_test.go`:

```go
"crypto/ed25519"
"encoding/hex"
"time"

jwtgo "github.com/golang-jwt/jwt/v5"
"github.com/prometheus/client_golang/prometheus/testutil"
```

- [ ] **Step 2: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestApiServer_initHandlers_ValidTokenPopulatesWhoami|TestApiServer_authenticatedRequestKeepsRouteMetricLabel' -v`
Expected: PASS. If `...KeepsRouteMetricLabel` FAILS with `got == 0` / `unknown == 1`, Auth was hoisted above the mux (forking before routing) — it must stay a per-route wrap.

- [ ] **Step 3: Run the full api package tests**

Run: `go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/serve_test.go
git commit -m "test(auth): verify valid token + route metric label under per-route auth (#114)"
```

---

### Task 4: Full verification, lint, and runbook reconciliation

**Files:** none (verification + process)

- [ ] **Step 1: Run the whole test suite**

Run: `go test ./...`
Expected: PASS. (Integration tests under `internal/integrationtests` use testcontainers and may be skipped/gated in CI; run `go test ./internal/... -short` if the environment lacks Docker.)

- [ ] **Step 2: Lint**

Run: `golangci-lint run ./internal/api/...`
Expected: no findings. (Config: `.golangci.yml`.)

- [ ] **Step 3: Reconcile runbooks**

This change alters an operationally observable surface (under `--auth-mode strict`, all user-facing `/api/v1` routes now return 401 without a valid JWT; auth metrics are emitted for all user-facing routes). Invoke the `reconcile-runbook` skill to check `wallet-eng-runbooks` for drift. It exits cleanly with `nothing to reconcile` if no runbook references the changed surface, and only stages a branch (never pushes/opens a PR) — hand off to the user for review.

- [ ] **Step 4: Confirm the ticket-body deviation is captured**

The design deliberately exempts `/rpc-health` from gating, which deviates from #114's body. Ensure the PR description notes this deviation and the follow-up to check `stellar/kube` for probes on `/rpc-health`. (Reference: the "Deviation from the ticket body" section of the spec.)

---

## Notes for the implementer

- The order of route registration is preserved from the original `initHandlers`; only `HandleFunc(...)` → `Handle(..., authed(...))` for user-facing routes and the relocation of `verifier`/introduction of `authed` change.
- `internal/api/middleware` and `internal/auth` are already imported in `serve.go`; no import changes there.
- Do not touch `initMiddleware` — `Auth` is intentionally *not* in the global chain.
- `/api/v1/feature-flags` is the canonical invoked test route: `GetFeatureFlags` has no file/service dependencies and returns 200 for a plain GET, so tests isolate auth behavior. Avoid `/api/v1/protocols` for invocation tests — its handler reads `ProtocolsConfigPath` per request and there is no `internal/api/testdata/protocols.json` (sibling tests only check registration, never invoke it), so it would 404.
- Routes that invoke `s.rpcService`/`s.walletBackendService`/`s.pricesService` (collectibles, ledger-key, balances, token-prices, account-history) would panic on the nil services left by `newTestAPIServer`; don't invoke them in these unit tests.
