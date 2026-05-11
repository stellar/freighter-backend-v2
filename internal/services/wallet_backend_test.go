// ABOUTME: Unit tests for wallet backend service error classification, address-scope policy, and balances fan-out.
// ABOUTME: Uses httptest.Server fakes to exercise GetBalancesByAccountAddresses without a real wallet-backend.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestGetHealth_HTTPErrorClassification(t *testing.T) {
	// Verify that the UpstreamError created by GetHealth for non-200 responses
	// classifies correctly with the HTTP status code as a sub-label.
	healthErr := &metrics.UpstreamError{
		Kind: "http_error",
		Code: 503,
		Err:  fmt.Errorf("health endpoint returned status 503"),
	}

	assert.Equal(t, "http_error:503", metrics.ClassifyError(healthErr))

	var upErr *metrics.UpstreamError
	require.True(t, errors.As(healthErr, &upErr))
	assert.Equal(t, "http_error", upErr.Kind)
	assert.Equal(t, 503, upErr.Code)
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
// missing "after" key surfaces as nil here — not pointer-to-empty). If body
// is empty and status is 0 the fake writes a default empty-balances
// success response.
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
		if status == 0 && payload == "" {
			status = http.StatusOK
			payload = emptyBalancesGraphQLResponse()
		}
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

// newTestWalletBackendService builds a walletBackendService directly,
// bypassing NewWalletBackendService so we can wire a no-signer wbclient
// pointed at the fake server. Tests live in the services package, so the
// unexported struct is accessible.
func newTestWalletBackendService(baseURL string, maxConcurrency int) *walletBackendService {
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
	addrC = "GCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCK6T"
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
		svc := newTestWalletBackendService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 2)
		assert.Equal(t, addrA, results[0].Address)
		assert.Equal(t, addrB, results[1].Address)
		assert.Nil(t, results[0].Error)
		assert.Nil(t, results[1].Error)
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
		svc := newTestWalletBackendService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 1)
		assert.Equal(t, addrA, results[0].Address)
		assert.Nil(t, results[0].Error)
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
		svc := newTestWalletBackendService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err)
		assert.Nil(t, raw)
		var upErr *metrics.UpstreamError
		require.True(t, errors.As(err, &upErr))
		assert.Equal(t, "graphql_error", upErr.Kind)
	})

	t.Run("account_not_found_returns_per_account_error", func(t *testing.T) {
		// wallet-backend signals "account not indexed" with a 200 response
		// carrying accountByAddress:null. wbclient surfaces that as the typed
		// sentinel ErrAccountNotFound (PR #612). It must remain address-scoped
		// — failing the whole batch when one of N requested accounts isn't
		// indexed would defeat the multi-account endpoint's purpose.
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			if address == addrA {
				return http.StatusOK, accountNotFoundGraphQLResponse()
			}
			return http.StatusOK, nativeBalanceGraphQLResponse("25.0000000")
		})
		svc := newTestWalletBackendService(f.server.URL, 10)

		raw, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.NoError(t, err)
		results := raw.([]*types.AccountBalances)
		require.Len(t, results, 2)
		require.NotNil(t, results[0].Error)
		// The per-account error string is the raw wbclient error, whose
		// chain contains the typed sentinel's "account not found" message.
		assert.Contains(t, *results[0].Error, "account not found")
		assert.Empty(t, results[0].Balances) // initialized to non-nil empty slice
		assert.Nil(t, results[1].Error)
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
		svc := newTestWalletBackendService(f.server.URL, 10)

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
		svc := newTestWalletBackendService(f.server.URL, 10)

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
		svc := newTestWalletBackendService(f.server.URL, 10)
		// Close before calling so every request errors at the transport layer
		// (connection refused). Transport errors must propagate top-level —
		// turning them into per-account strings would hide outages.
		f.server.Close()

		_, err := svc.GetBalancesByAccountAddresses(context.Background(), []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err)
	})

	t.Run("zero_balances_marshals_as_empty_array_not_null", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) { return 0, "" }) // default → empty edges
		svc := newTestWalletBackendService(f.server.URL, 10)

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
		svc := newTestWalletBackendService(f.server.URL, limit)

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
		svc := newTestWalletBackendService(f.server.URL, 10)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := svc.GetBalancesByAccountAddresses(ctx, []string{addrA, addrB}, types.PUBLIC)
		require.Error(t, err, "ctx timeout must surface as top-level error, not per-account strings")
	})

	t.Run("duplicate_addresses_collapse_preserving_first_seen_order", func(t *testing.T) {
		f := newFanoutFakeServer(t, func(address string, _ *string) (int, string) {
			return http.StatusOK, nativeBalanceGraphQLResponse("1.0000000")
		})
		svc := newTestWalletBackendService(f.server.URL, 10)

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
