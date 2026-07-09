// ABOUTME: Unit tests for the wallet-backend service constructor, error classification, balances fan-out, and account history.
// ABOUTME: Uses httptest.Server fakes to exercise service methods without a real wallet-backend.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/wallet-backend/pkg/wbclient"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestClassifyWBError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectWrapped bool
		expectedKind  string
		expectedCode  int
	}{
		{
			name:          "GraphQL error wraps as graphql_error with no code",
			err:           fmt.Errorf("GraphQL error: something went wrong"),
			expectWrapped: true,
			expectedKind:  "graphql_error",
			expectedCode:  0,
		},
		{
			name:          "HTTP 500 wraps as http_error with parsed code",
			err:           fmt.Errorf("unexpected statusCode=500, body=internal server error"),
			expectWrapped: true,
			expectedKind:  "http_error",
			expectedCode:  500,
		},
		{
			name:          "HTTP 404 wraps as http_error with parsed code",
			err:           fmt.Errorf("unexpected statusCode=404, body=not found"),
			expectWrapped: true,
			expectedKind:  "http_error",
			expectedCode:  404,
		},
		{
			name:          "generic error passes through unchanged",
			err:           fmt.Errorf("some other error"),
			expectWrapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyWBError(tt.err)
			if tt.expectWrapped {
				var upErr *metrics.UpstreamError
				require.True(t, errors.As(result, &upErr), "expected UpstreamError, got %T", result)
				assert.Equal(t, tt.expectedKind, upErr.Kind)
				assert.Equal(t, tt.expectedCode, upErr.Code)
			} else {
				assert.Equal(t, tt.err, result)
			}
		})
	}
}

func TestNewWalletBackendService_ValidatesConcurrency(t *testing.T) {
	// Zero/negative concurrency is a misconfiguration that would silently
	// disable the fan-out (errgroup with SetLimit(0) blocks forever), so the
	// constructor must reject it at startup.
	_, err := NewWalletBackendService("", "", "", "", 0, nil)
	require.Error(t, err, "0 must be rejected")
	assert.Contains(t, err.Error(), "must be > 0")

	_, err = NewWalletBackendService("", "", "", "", -1, nil)
	require.Error(t, err, "negative values must be rejected")

	_, err = NewWalletBackendService("", "", "", "", 1, nil)
	require.NoError(t, err, "positive value must be accepted")
}

// fanoutFakeServer encapsulates an httptest server that responds to wbclient
// GetAccountBalances calls. The handler dispatches based on the requested
// address (parsed out of the GraphQL variables) so a single test can mix
// success/failure responses across addresses.
type fanoutFakeServer struct {
	server   *httptest.Server
	calls    atomic.Int64
	inflight atomic.Int32
	peak     atomic.Int32
}

// fanoutResponder returns the body and status the fake should write for a
// given requested address and pagination cursor. The after argument is nil
// for first-page requests (per wbclient's buildPaginationVars, nil
// pagination vars are omitted from the GraphQL request map entirely, so a
// missing "after" key surfaces as nil here — not pointer-to-empty).
type fanoutResponder func(address string, after *string) (status int, body string)

func newFanoutFakeServer(t *testing.T, respond fanoutResponder) *fanoutFakeServer {
	t.Helper()
	f := &fanoutFakeServer{}
	mux := http.NewServeMux()
	// wbclient posts every query (mutations and queries) to /graphql/query.
	mux.HandleFunc("/graphql/query", func(w http.ResponseWriter, r *http.Request) {
		// Track concurrency for the bounded-concurrency assertion. We update
		// before reading the body so the peak reflects work done in parallel
		// inside the handler, not just the request arrival rate.
		cur := f.inflight.Add(1)
		defer f.inflight.Add(-1)
		for {
			old := f.peak.Load()
			if cur <= old || f.peak.CompareAndSwap(old, cur) {
				break
			}
		}
		f.calls.Add(1)

		body, _ := io.ReadAll(r.Body)
		address, after := extractAddressFromGraphQLBody(t, body)

		status, payload := respond(address, after)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// extractAddressFromGraphQLBody pulls the "address" variable and the
// optional "after" pagination cursor from a wbclient GraphQL request body.
// wbclient's executeGraphQL marshals as {"query": "...", "variables":
// {"address": "G...", "after": "cursor"}} — and per buildPaginationVars,
// nil pagination args are omitted from the variables map entirely. So a
// missing "after" key surfaces here as a nil pointer (first page), distinct
// from a present-but-empty cursor.
func extractAddressFromGraphQLBody(t *testing.T, body []byte) (string, *string) {
	t.Helper()
	var req struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal graphql request body: %v (body=%q)", err, string(body))
	}
	addr, _ := req.Variables["address"].(string)
	var after *string
	if v, ok := req.Variables["after"]; ok {
		if s, ok := v.(string); ok {
			after = &s
		}
	}
	return addr, after
}

// emptyBalancesGraphQLResponse returns a GraphQL response with an empty
// balances connection. Always-non-nil "edges": [] mirrors how wallet-backend
// would respond for an account with no balances.
func emptyBalancesGraphQLResponse() string {
	return `{"data":{"accountByAddress":{"balances":{"edges":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`
}

// accountNotFoundGraphQLResponse returns a 200 response with a null
// accountByAddress. wallet-backend uses this shape (rather than a GraphQL
// errors[] entry or an HTTP 404) to signal that the requested account has
// not been indexed; wbclient surfaces it as ErrAccountNotFound.
func accountNotFoundGraphQLResponse() string {
	return `{"data":{"accountByAddress":null}}`
}

// nativeBalanceGraphQLResponse returns a GraphQL response with one
// NativeBalance edge. The __typename field is mandatory — wbclient's
// polymorphic UnmarshalBalance keys off it and errors with "unknown balance
// type" if it's missing.
func nativeBalanceGraphQLResponse(amount string) string {
	return fmt.Sprintf(`{
		"data": {
			"accountByAddress": {
				"balances": {
					"edges": [{
						"node": {
							"__typename": "NativeBalance",
							"balance": %q,
							"tokenId": "native",
							"tokenType": "NATIVE",
							"minimumBalance": "1.0000000",
							"buyingLiabilities": "0.0000000",
							"sellingLiabilities": "0.0000000",
							"lastModifiedLedger": 100
						}
					}],
					"pageInfo": {"hasNextPage": false, "endCursor": null}
				}
			}
		}
	}`, amount)
}

// graphqlErrorResponse returns a 200 response carrying a GraphQL error.
// wbclient surfaces these as `GraphQL error: ...` Go errors.
func graphqlErrorResponse(message string) string {
	return fmt.Sprintf(`{"errors":[{"message":%q}]}`, message)
}

// paginatedNativeBalanceResponse returns a GraphQL response with one
// NativeBalance edge plus a configurable PageInfo. Use it to assemble
// multi-page replies when testing the SDK's internal pagination loop:
// page 1 with hasNext=true and a non-empty endCursor, then a follow-up
// request keyed off that cursor.
func paginatedNativeBalanceResponse(amount, endCursor string, hasNext bool) string {
	cursorJSON := "null"
	if endCursor != "" {
		cursorJSON = fmt.Sprintf("%q", endCursor)
	}
	return fmt.Sprintf(`{
		"data": {
			"accountByAddress": {
				"balances": {
					"edges": [{
						"node": {
							"__typename": "NativeBalance",
							"balance": %q,
							"tokenId": "native",
							"tokenType": "NATIVE",
							"minimumBalance": "1.0000000",
							"buyingLiabilities": "0.0000000",
							"sellingLiabilities": "0.0000000",
							"lastModifiedLedger": 100
						}
					}],
					"pageInfo": {"hasNextPage": %t, "endCursor": %s}
				}
			}
		}
	}`, amount, hasNext, cursorJSON)
}

// newFanoutTestService builds a walletBackendService by direct struct construction
// so the fan-out tests below can access internal atomics on fanoutFakeServer
// (peak / calls / inflight). New tests that don't need direct struct access
// should use newTestWalletBackendService instead — it exercises the real
// NewWalletBackendService constructor and returns the interface type.
func newFanoutTestService(baseURL string, maxConcurrency int) *walletBackendService {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	client := wbclient.NewClient(baseURL, nil) // nil signer is fine — wbclient skips signing when nil
	client.HTTPClient = httpClient
	return &walletBackendService{
		pubnetClient:          client,
		httpClient:            httpClient,
		svcMetrics:            metrics.NewService(prometheus.NewRegistry()),
		maxBalanceConcurrency: maxConcurrency,
	}
}

const (
	// Realistic-looking valid Stellar G-address shapes for tests. The fake
	// server doesn't validate them; they just need to be distinct strings the
	// fake can dispatch on.
	addrA = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	addrB = "GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBLNG"
)

func TestGetBalancesByAccountAddresses_FanOut(t *testing.T) {
	t.Run("success_path_preserves_input_order", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			switch address {
			case addrA:
				return http.StatusOK, nativeBalanceGraphQLResponse("100.0000000")
			case addrB:
				return http.StatusOK, nativeBalanceGraphQLResponse("200.0000000")
			}
			t.Fatalf("unexpected address %q", address)
			return 0, ""
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 2)
		assert.Equal(t, addrA, results[0].Address)
		assert.Equal(t, addrB, results[1].Address)
		assert.Len(t, results[0].Balances, 1)
		assert.Len(t, results[1].Balances, 1)
	})

	t.Run("multi_page_account_aggregates_all_pages_via_sdk_loop", func(t *testing.T) {
		// Regression-defense for the GetAllAccountBalances swap: if someone
		// later swaps it back to GetAccountBalances (the explicit-page form)
		// without adding a loop, only the first page's balances would land
		// in the response and this assertion would fail. Page 1 returns one
		// balance with hasNext=true; page 2 returns a second balance with
		// hasNext=false. The fake dispatches by the after cursor.
		const cursor = "page-1-end-cursor"
		f := newFanoutFakeServer(t, func(address string, after *string) (int, string) {
			if address != addrA {
				t.Fatalf("unexpected address %q", address)
			}
			if after == nil {
				return http.StatusOK, paginatedNativeBalanceResponse("10.0000000", cursor, true)
			}
			if *after != cursor {
				t.Fatalf("unexpected after cursor %q (want %q)", *after, cursor)
			}
			return http.StatusOK, paginatedNativeBalanceResponse("20.0000000", "", false)
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 1)
		assert.Equal(t, addrA, results[0].Address)
		require.Len(t, results[0].Balances, 2, "SDK pagination loop must aggregate edges across pages")
		assert.EqualValues(t, int64(2), f.calls.Load(), "exactly one request per page")
	})

	t.Run("graphql_error_fails_request_systemically", func(t *testing.T) {
		// A GraphQL errors[] payload from the server is most likely systemic
		// (schema/query/resolver bug affecting every account in the fan-out)
		// and the SDK exposes no structured signal to prove account-locality
		// — only the first error's message is stringified. Surface as a
		// top-level error so monitoring sees the outage instead of a 200 of
		// per-account error strings. The one demonstrably address-scoped
		// case (accountByAddress:null) is the typed wbclient.ErrAccountNotFound
		// sentinel, covered by the next subtest.
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			if address == addrA {
				return http.StatusOK, graphqlErrorResponse("schema validation failed")
			}
			return http.StatusOK, nativeBalanceGraphQLResponse("50.0000000")
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err)
		assert.Nil(t, raw)
		var upErr *metrics.UpstreamError
		require.True(t, errors.As(err, &upErr))
		assert.Equal(t, "graphql_error", upErr.Kind)
	})

	t.Run("account_not_found_is_address_scoped", func(t *testing.T) {
		// wallet-backend signals "account not indexed" with a 200 response
		// carrying accountByAddress:null. wbclient surfaces that as the typed
		// sentinel ErrAccountNotFound (PR #612). It must remain address-scoped
		// — failing the whole batch when one of N requested accounts isn't
		// indexed would defeat the multi-account endpoint's purpose. The
		// not-found account is reported via is_funded=false, not a batch error.
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			if address == addrA {
				return http.StatusOK, accountNotFoundGraphQLResponse()
			}
			return http.StatusOK, nativeBalanceGraphQLResponse("25.0000000")
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 2)
		assert.False(t, results[0].IsFunded, "not-found account is reported unfunded")
		assert.Empty(t, results[0].Balances) // initialized to non-nil empty slice
		assert.True(t, results[1].IsFunded, "the other account still resolves")
		assert.Len(t, results[1].Balances, 1)
	})

	t.Run("http_4xx_fails_request_systemically", func(t *testing.T) {
		// wbclient POSTs every call to /graphql/query. An HTTP 4xx from the
		// upstream is therefore a transport-level failure (wrong base URL,
		// missing route, auth/signing, rate limit) — not "this account
		// doesn't exist" (which arrives as a 200 + accountByAddress:null and
		// is surfaced as wbclient.ErrAccountNotFound). 4xx must fail the
		// whole request so monitoring sees the outage instead of a 200 of
		// per-account error strings.
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			if address == addrA {
				return http.StatusNotFound, `not found`
			}
			return http.StatusOK, nativeBalanceGraphQLResponse("75.0000000")
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err)
		assert.Nil(t, raw)
		var upErr *metrics.UpstreamError
		require.True(t, errors.As(err, &upErr))
		assert.Equal(t, "http_error", upErr.Kind)
		assert.Equal(t, 404, upErr.Code)
	})

	t.Run("systemic_http_5xx_fails_request", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			if address == addrA {
				return http.StatusServiceUnavailable, "wallet-backend down"
			}
			return http.StatusOK, nativeBalanceGraphQLResponse("100.0000000")
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err, "5xx must surface as a top-level error so monitoring catches outages")
		assert.Nil(t, raw)
		var upErr *metrics.UpstreamError
		require.True(t, errors.As(err, &upErr))
		assert.Equal(t, "http_error", upErr.Kind)
		assert.Equal(t, 503, upErr.Code)
	})

	t.Run("systemic_transport_error_fails_request", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			return http.StatusOK, nativeBalanceGraphQLResponse("100.0000000")
		})
		svc := newFanoutTestService(f.server.URL, 10)
		// Close before calling so every request errors at the transport layer
		// (connection refused). Transport errors must propagate top-level —
		// turning them into per-account strings would hide outages.
		f.server.Close()

		_, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err)
	})

	t.Run("zero_balances_marshals_as_empty_array_not_null", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(_ string, _ *string) (int, string) {
			return http.StatusOK, emptyBalancesGraphQLResponse()
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 1)
		assert.NotNil(t, results[0].Balances, "Balances must be a non-nil slice for empty accounts")
		assert.Empty(t, results[0].Balances)

		// Nail down the wire format — a regression to "balances":null would be
		// a behavior change for API consumers.
		jsonBytes, err := json.Marshal(results[0])
		require.NoError(t, err)
		assert.Contains(t, string(jsonBytes), `"balances":[]`)
		assert.NotContains(t, string(jsonBytes), `"balances":null`)
	})

	t.Run("bounded_concurrency_respects_limit", func(t *testing.T) {
		const limit = 2
		const total = 6
		barrier := make(chan struct{}, total)
		release := make(chan struct{})

		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			// Signal arrival, then block until released. This forces the test
			// to observe the true concurrent ceiling rather than the rate at
			// which goroutines happen to be scheduled.
			barrier <- struct{}{}
			<-release
			return http.StatusOK, nativeBalanceGraphQLResponse("1.0000000")
		})
		svc := newFanoutTestService(f.server.URL, limit)

		addrs := []string{
			"GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			"GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBLNG",
			"GCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCK6T",
			"GDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDPGE",
			"GEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEERVZ",
			"GFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFB7M",
		}
		require.Len(t, addrs, total)

		done := make(chan error, 1)
		go func() {
			_, err := svc.GetBalancesByAccountAddresses(context.Background(), addrs, types.PUBLIC)
			done <- err
		}()

		// Wait for `limit` goroutines to be parked at the barrier. If errgroup
		// admitted more than `limit`, we'd see >`limit` arrivals before any
		// release happens.
		for i := 0; i < limit; i++ {
			select {
			case <-barrier:
			case <-time.After(2 * time.Second):
				t.Fatalf("only %d goroutines arrived at the barrier (expected %d)", i, limit)
			}
		}
		// Give a beat for any over-admission to surface.
		select {
		case <-barrier:
			t.Fatalf("more than %d goroutines admitted concurrently", limit)
		case <-time.After(100 * time.Millisecond):
		}

		// Now drain by releasing in waves of `limit`.
		remaining := total - limit
		for remaining > 0 {
			batch := limit
			if remaining < batch {
				batch = remaining
			}
			for i := 0; i < batch; i++ {
				release <- struct{}{}
			}
			for i := 0; i < batch; i++ {
				<-barrier
			}
			remaining -= batch
		}
		// Release the last batch (the initial `limit` goroutines that arrived
		// first plus any final stragglers — total releases must equal total).
		for i := 0; i < total; i++ {
			select {
			case release <- struct{}{}:
			default:
				// channel full means a goroutine already grabbed it
			}
		}
		close(release)

		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("fan-out did not complete after release")
		}

		assert.LessOrEqual(t, f.peak.Load(), int32(limit), "peak in-flight must respect the per-request limit")
		assert.Equal(t, int64(total), f.calls.Load(), "every unique address must be queried exactly once")
	})

	t.Run("request_level_timeout_returns_top_level_error", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			// Sleep longer than the test ctx timeout so the in-flight call
			// observes ctx.Err() and bubbles it up.
			time.Sleep(500 * time.Millisecond)
			return http.StatusOK, nativeBalanceGraphQLResponse("1.0000000")
		})
		svc := newFanoutTestService(f.server.URL, 10)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := svc.GetBalancesByAccountAddresses(ctx, []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err, "ctx timeout must surface as top-level error, not per-account strings")
	})

	t.Run("duplicate_addresses_collapse_preserving_first_seen_order", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			return http.StatusOK, nativeBalanceGraphQLResponse("1.0000000")
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(
			context.Background(),
			[]string{addrA, addrB, addrA, addrA},
			types.PUBLIC,
		)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 2, "duplicates must collapse")
		assert.Equal(t, addrA, results[0].Address, "first-seen ordering preserved")
		assert.Equal(t, addrB, results[1].Address)
		assert.Equal(t, int64(2), f.calls.Load(), "fake server must see exactly len(unique) GraphQL calls")
	})
}

func TestGetBalancesByAccountAddresses_EnvelopeEnrichment(t *testing.T) {
	t.Run("funded_account_sets_is_funded_and_hoists_subentry_count", func(t *testing.T) {
		// A native balance carrying numSubentries=5. The service must set
		// is_funded=true, hoist subentry_count from the native entry, and map
		// the balance into a *NativeBalance with available = balance -
		// minimumBalance (100 - 2.5 = 97.5).
		nativeWithSubentries := `{
			"data": {"accountByAddress": {"balances": {
				"edges": [{"node": {
					"__typename": "NativeBalance",
					"balance": "100.0000000", "tokenId": "native", "tokenType": "NATIVE",
					"minimumBalance": "2.5000000", "buyingLiabilities": "0.0000000", "sellingLiabilities": "0.0000000",
					"lastModifiedLedger": 100, "numSubentries": 5
				}}],
				"pageInfo": {"hasNextPage": false, "endCursor": null}
			}}}
		}`
		f := newFanoutFakeServer(t, func(_ string, _ *string) (int, string) {
			return http.StatusOK, nativeWithSubentries
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 1)
		assert.True(t, results[0].IsFunded)
		assert.EqualValues(t, 5, results[0].SubentryCount)
		require.Len(t, results[0].Balances, 1)
		native, ok := results[0].Balances[0].(*types.NativeBalance)
		require.True(t, ok, "expected *NativeBalance, got %T", results[0].Balances[0])
		assert.Equal(t, "97.5000000", native.Available)
	})

	t.Run("funded_account_surfaces_native_and_non_native_balances", func(t *testing.T) {
		// A funded account must surface ALL its balances, not just the native entry
		// that drives is_funded. A native + SEP-41 fixture guards against the funded
		// path accidentally being narrowed to native-only.
		nativeAndSep41 := `{
			"data": {"accountByAddress": {"balances": {
				"edges": [
					{"node": {
						"__typename": "NativeBalance",
						"balance": "100.0000000", "tokenId": "native", "tokenType": "NATIVE",
						"minimumBalance": "1.0000000", "buyingLiabilities": "0.0000000", "sellingLiabilities": "0.0000000",
						"lastModifiedLedger": 100, "numSubentries": 3
					}},
					{"node": {
						"__typename": "SEP41Balance",
						"balance": "5000000000", "tokenId": "CDMLFMKMMD7MWZP3FKUBZPVHTUEDLSX4BYGYKH4GCESXYHS3IHQ4EIG4",
						"tokenType": "SEP41", "name": "SEP41 Token", "symbol": "SEP41",
						"decimals": 7, "lastModifiedLedger": 12345
					}}
				],
				"pageInfo": {"hasNextPage": false, "endCursor": null}
			}}}
		}`
		f := newFanoutFakeServer(t, func(_ string, _ *string) (int, string) {
			return http.StatusOK, nativeAndSep41
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 1)
		assert.True(t, results[0].IsFunded)
		assert.EqualValues(t, 3, results[0].SubentryCount)
		require.Len(t, results[0].Balances, 2, "funded account must surface all balances, not just native")
		var sawNative, sawSEP41 bool
		for _, b := range results[0].Balances {
			switch b.(type) {
			case *types.NativeBalance:
				sawNative = true
			case *types.SEP41Balance:
				sawSEP41 = true
			}
		}
		assert.True(t, sawNative, "native balance must be surfaced")
		assert.True(t, sawSEP41, "non-native (SEP-41) balance must be surfaced for a funded account")
	})

	t.Run("account_not_found_sets_is_funded_false", func(t *testing.T) {
		// ErrAccountNotFound is address-scoped: is_funded=false,
		// subentry_count=0, and an empty balances slice (no per-account error —
		// not-found is conveyed by is_funded).
		f := newFanoutFakeServer(t, func(_ string, _ *string) (int, string) {
			return http.StatusOK, accountNotFoundGraphQLResponse()
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 1)
		assert.False(t, results[0].IsFunded)
		assert.EqualValues(t, 0, results[0].SubentryCount)
		assert.Empty(t, results[0].Balances)
	})

	t.Run("unfunded_account_with_only_contract_balance_hides_balances", func(t *testing.T) {
		// A successful fetch returning only a SEP-41 (Soroban) balance and no native
		// balance means the account has no classic account: is_funded=false,
		// subentry_count=0, and no balances surfaced (an unfunded account exposes no
		// balances). "Fetch succeeded" must not be mistaken for "funded".
		sep41Only := `{
			"data": {"accountByAddress": {"balances": {
				"edges": [{"node": {
					"__typename": "SEP41Balance",
					"balance": "5000000000", "tokenId": "CDMLFMKMMD7MWZP3FKUBZPVHTUEDLSX4BYGYKH4GCESXYHS3IHQ4EIG4",
					"tokenType": "SEP41", "name": "SEP41 Token", "symbol": "SEP41",
					"decimals": 7, "lastModifiedLedger": 12345
				}}],
				"pageInfo": {"hasNextPage": false, "endCursor": null}
			}}}
		}`
		f := newFanoutFakeServer(t, func(_ string, _ *string) (int, string) {
			return http.StatusOK, sep41Only
		})
		svc := newFanoutTestService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 1)
		assert.False(t, results[0].IsFunded, "no native balance -> unfunded even though the fetch succeeded")
		assert.EqualValues(t, 0, results[0].SubentryCount)
		assert.Empty(t, results[0].Balances, "unfunded account exposes no balances")
	})
}

// txResponder builds a fake response for the GetAccountTransactions GraphQL query.
type txResponder func(address string, vars map[string]interface{}) (status int, body string)

// newTxFakeServer creates an httptest.Server that routes GraphQL requests through a txResponder.
// It unmarshals the full variables map so tests can assert on any variable (first, last, after,
// before, since, until) without a separate extractor helper.
func newTxFakeServer(t *testing.T, respond txResponder) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql/query", func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req wbclient.GraphQLRequest
		require.NoError(t, json.Unmarshal(bodyBytes, &req))
		addr, _ := req.Variables["address"].(string)
		status, body := respond(addr, req.Variables)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return httptest.NewServer(mux)
}

func TestGetAccountTransactions(t *testing.T) {
	const addr = "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF"

	// nestedEdgeJSON is one edge of the AccountTransactionConnection. operations
	// and stateChanges MUST be non-null — the SDK's UnmarshalJSON rejects nulls.
	const happyBody = `{
		"data": {"accountByAddress": {"transactions": {
			"edges": [
				{"cursor": "cur-1", "node": {"hash": "h1", "feeCharged": 100, "resultCode": "tx_success", "ledgerNumber": 42, "ledgerCreatedAt": "2026-01-01T00:00:00Z", "isFeeBump": false, "ingestedAt": "2026-01-01T00:00:01Z"},
				 "operations": [{"id": 7, "operationType": "PAYMENT", "operationXdr": "AAA", "resultCode": "op_success", "successful": true, "ledgerNumber": 42, "ledgerCreatedAt": "2026-01-01T00:00:00Z", "ingestedAt": "2026-01-01T00:00:01Z"}],
				 "stateChanges": [{"__typename": "StandardBalanceChange", "type": "BALANCE", "reason": "DEBIT", "ledgerNumber": 42, "ledgerCreatedAt": "2026-01-01T00:00:00Z", "ingestedAt": "2026-01-01T00:00:01Z", "standardBalanceTokenId": "native", "amount": "10"}]}
			],
			"pageInfo": {"startCursor": "cur-1", "endCursor": "cur-1", "hasNextPage": true, "hasPreviousPage": true}
		}}}
	}`

	t.Run("happy path maps nested edges into AccountTransaction", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, vars map[string]interface{}) (int, string) {
			assert.EqualValues(t, 20, vars["first"])
			assert.Equal(t, "cur-prev", vars["after"])
			return 200, happyBody
		})
		defer server.Close()

		svc := newTestWalletBackendService(t, server.URL)
		cursor := "cur-prev"
		got, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{
			Limit: 20, Cursor: &cursor, Direction: types.PaginationDirectionNext,
		})
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Len(t, got.Data, 1)
		assert.Equal(t, "h1", got.Data[0].Hash)
		require.Len(t, got.Data[0].Operations, 1)
		assert.Equal(t, int64(7), got.Data[0].Operations[0].ID)
		assert.Equal(t, "PAYMENT", got.Data[0].Operations[0].OperationType)
		require.Len(t, got.Data[0].StateChanges, 1)
		require.IsType(t, &types.StandardBalanceChange{}, got.Data[0].StateChanges[0])
		assert.True(t, got.Pagination.HasNext)
		require.NotNil(t, got.Pagination.NextCursor)
		assert.Equal(t, "cur-1", *got.Pagination.NextCursor)
	})

	t.Run("direction=prev translates to last/before", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, vars map[string]interface{}) (int, string) {
			assert.EqualValues(t, 5, vars["last"])
			assert.Equal(t, "cur-end", vars["before"])
			return 200, `{"data":{"accountByAddress":{"transactions":{"edges":[],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false}}}}}`
		})
		defer server.Close()
		svc := newTestWalletBackendService(t, server.URL)
		cursor := "cur-end"
		_, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 5, Cursor: &cursor, Direction: types.PaginationDirectionPrev})
		require.NoError(t, err)
	})

	t.Run("accountByAddress null returns ErrAccountNotFound", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, _ map[string]interface{}) (int, string) {
			return 200, `{"data":{"accountByAddress":null}}`
		})
		defer server.Close()
		svc := newTestWalletBackendService(t, server.URL)
		_, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 20, Direction: types.PaginationDirectionNext})
		require.Error(t, err)
		assert.True(t, errors.Is(err, wbclient.ErrAccountNotFound))
	})

	t.Run("GraphQL errors are wrapped as graphql_error", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, _ map[string]interface{}) (int, string) {
			return 200, `{"errors":[{"message":"schema bug"}]}`
		})
		defer server.Close()
		svc := newTestWalletBackendService(t, server.URL)
		_, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 20, Direction: types.PaginationDirectionNext})
		require.Error(t, err)
		var upErr *metrics.UpstreamError
		require.True(t, errors.As(err, &upErr))
		assert.Equal(t, "graphql_error", upErr.Kind)
	})

	t.Run("forwards since and until to wbclient when provided", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, vars map[string]interface{}) (int, string) {
			assert.Equal(t, "2026-01-01T00:00:00Z", vars["since"])
			assert.Equal(t, "2026-02-01T00:00:00Z", vars["until"])
			return 200, `{"data":{"accountByAddress":{"transactions":{"edges":[],"pageInfo":{"hasNextPage":false,"hasPreviousPage":false}}}}}`
		})
		defer server.Close()
		svc := newTestWalletBackendService(t, server.URL)
		since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		_, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 20, Direction: types.PaginationDirectionNext, Since: &since, Until: &until})
		require.NoError(t, err)
	})

	t.Run("transport failure surfaces unwrapped (handler maps it to 502)", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		serverURL := server.URL
		server.Close()
		svc := newTestWalletBackendService(t, serverURL)
		_, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 20, Direction: types.PaginationDirectionNext})
		require.Error(t, err)
		var upErr *metrics.UpstreamError
		assert.False(t, errors.As(err, &upErr), "expected unwrapped transport error, got %T", err)
		var urlErr *url.Error
		assert.True(t, errors.As(err, &urlErr), "expected *url.Error in chain, got %T", err)
	})

	t.Run("HTTP 503 from upstream is wrapped as http_error with code", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, _ map[string]interface{}) (int, string) { return 503, `service unavailable` })
		defer server.Close()
		svc := newTestWalletBackendService(t, server.URL)
		_, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 20, Direction: types.PaginationDirectionNext})
		require.Error(t, err)
		var upErr *metrics.UpstreamError
		require.True(t, errors.As(err, &upErr))
		assert.Equal(t, "http_error", upErr.Kind)
		assert.Equal(t, 503, upErr.Code)
	})

	t.Run("null transactions connection returns empty page", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, _ map[string]interface{}) (int, string) {
			return 200, `{"data":{"accountByAddress":{"transactions":null}}}`
		})
		defer server.Close()
		svc := newTestWalletBackendService(t, server.URL)
		got, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 20, Direction: types.PaginationDirectionNext})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, []*types.AccountTransaction{}, got.Data)
		assert.False(t, got.Pagination.HasNext)
	})

	t.Run("ErrAccountNotFound does not increment ErrorsTotal", func(t *testing.T) {
		t.Parallel()
		server := newTxFakeServer(t, func(_ string, _ map[string]interface{}) (int, string) {
			return 200, `{"data":{"accountByAddress":null}}`
		})
		defer server.Close()
		reg := prometheus.NewRegistry()
		svcMetrics := metrics.NewService(reg)
		svc := newTestWalletBackendServiceWithMetrics(t, server.URL, svcMetrics)
		_, err := svc.GetAccountTransactions(context.Background(), addr, types.PUBLIC, types.AccountHistoryParams{Limit: 20, Direction: types.PaginationDirectionNext})
		require.Error(t, err)
		assert.True(t, errors.Is(err, wbclient.ErrAccountNotFound))
		assert.Equal(t, float64(1), testutilCounterValue(t, reg, "freighter_service_calls_total", map[string]string{"service": "wallet-backend", "method": "GetAccountTransactions", "network": types.PUBLIC}))
		assert.Equal(t, float64(0), testutilCounterValue(t, reg, "freighter_service_errors_total", nil))
	})
}

// newTestWalletBackendService builds a walletBackendService configured against the
// given pubnet URL, with no metrics (test helper for non-metrics tests).
func newTestWalletBackendService(t *testing.T, pubnetURL string) types.WalletBackendService {
	return newTestWalletBackendServiceWithMetrics(t, pubnetURL, nil)
}

// newTestWalletBackendServiceWithMetrics builds a walletBackendService configured against
// the given pubnet URL, wiring the provided metrics.Service (may be nil).
func newTestWalletBackendServiceWithMetrics(t *testing.T, pubnetURL string, svcMetrics *metrics.Service) types.WalletBackendService {
	t.Helper()
	const signingKey = "SBLIQC4PO4OJDNAUGJJL23H7HWME4VCW4PFAPIJ6SI4HHEYKJ2QO32HN"
	svc, err := NewWalletBackendService(pubnetURL, "", signingKey, "", 10, svcMetrics)
	require.NoError(t, err)
	return svc
}

// testutilCounterValue returns the summed value of a Prometheus counter (or
// CounterVec total when labels is nil) registered in the given Gatherer.
func testutilCounterValue(t *testing.T, g prometheus.Gatherer, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := g.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		var total float64
		for _, m := range mf.GetMetric() {
			if labels != nil {
				matched := true
				for k, v := range labels {
					found := false
					for _, lp := range m.GetLabel() {
						if lp.GetName() == k && lp.GetValue() == v {
							found = true
							break
						}
					}
					if !found {
						matched = false
						break
					}
				}
				if !matched {
					continue
				}
			}
			total += m.GetCounter().GetValue()
		}
		return total
	}
	return 0
}

func TestToPaginationInfo_NilSafe(t *testing.T) {
	t.Parallel()
	got := toPaginationInfo(nil)
	assert.Equal(t, types.PaginationInfo{}, got, "nil pi must return zero PaginationInfo")
}
