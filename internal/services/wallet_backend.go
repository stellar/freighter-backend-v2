// ABOUTME: Wallet-backend service implementation that wraps the wbclient SDK and exposes the methods used by API handlers.
// ABOUTME: Hosts the multi-account balances fan-out: per-request bounded concurrency over the per-address GraphQL query.
package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stellar/wallet-backend/pkg/wbclient"
	"github.com/stellar/wallet-backend/pkg/wbclient/auth"
	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"
	"golang.org/x/sync/errgroup"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	walletBackendServiceName = "wallet-backend"
)

// httpStatusCodeRegex extracts the numeric status code from wbclient's
// "unexpected statusCode=N, body=..." error string. wbclient builds the message
// with fmt.Errorf and no %w wrapping, so string parsing is the only option.
var httpStatusCodeRegex = regexp.MustCompile(`statusCode=(\d{3})`)

type walletBackendService struct {
	pubnetClient          *wbclient.Client
	testnetClient         *wbclient.Client
	httpClient            *http.Client
	svcMetrics            *metrics.Service
	maxBalanceConcurrency int
}

// NewWalletBackendService constructs a wallet-backend service backed by the
// shared wbclient HTTP client. maxBalanceConcurrency caps the per-request
// goroutine count for the multi-account balances fan-out and must be > 0.
func NewWalletBackendService(pubnetUrl, testnetUrl, pubnetSigningKey, testnetSigningKey string, maxBalanceConcurrency int, m *metrics.Service) (types.WalletBackendService, error) {
	if maxBalanceConcurrency <= 0 {
		return nil, fmt.Errorf("maxBalanceConcurrency must be > 0, got %d", maxBalanceConcurrency)
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			MaxConnsPerHost:       50,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     false,
			DisableCompression:    false,
			ForceAttemptHTTP2:     true,
		},
	}

	var pubnetClient *wbclient.Client
	var testnetClient *wbclient.Client

	// Initialize pubnet client if URL and signing key are provided
	if pubnetUrl != "" && pubnetSigningKey != "" {
		pubnetJWTGenerator, err := auth.NewJWTTokenGenerator(pubnetSigningKey)
		if err != nil {
			return nil, fmt.Errorf("creating pubnet JWT generator: %w", err)
		}
		pubnetSigner := auth.NewHTTPRequestSigner(pubnetJWTGenerator)
		pubnetClient = wbclient.NewClient(pubnetUrl, pubnetSigner)
		pubnetClient.HTTPClient = httpClient
	}

	// Initialize testnet client if URL and signing key are provided
	if testnetUrl != "" && testnetSigningKey != "" {
		testnetJWTGenerator, err := auth.NewJWTTokenGenerator(testnetSigningKey)
		if err != nil {
			return nil, fmt.Errorf("creating testnet JWT generator: %w", err)
		}
		testnetSigner := auth.NewHTTPRequestSigner(testnetJWTGenerator)
		testnetClient = wbclient.NewClient(testnetUrl, testnetSigner)
		testnetClient.HTTPClient = httpClient
	}

	return &walletBackendService{
		pubnetClient:          pubnetClient,
		testnetClient:         testnetClient,
		httpClient:            httpClient,
		svcMetrics:            m,
		maxBalanceConcurrency: maxBalanceConcurrency,
	}, nil
}

func (w *walletBackendService) Name() string {
	return walletBackendServiceName
}

func (w *walletBackendService) GetHealth(ctx context.Context, network string) (_ types.GetHealthResponse, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(w.svcMetrics, walletBackendServiceName, "GetHealth", network, time.Since(start).Seconds(), err)
	}()

	client := w.configureNetworkClient(network)
	if client == nil {
		return types.GetHealthResponse{Status: types.StatusError}, fmt.Errorf("wallet backend client not configured for network: %s", network)
	}

	// Make a GET request to the /health endpoint
	healthURL := client.BaseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return types.GetHealthResponse{Status: types.StatusError}, fmt.Errorf("creating health request: %w", err)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return types.GetHealthResponse{Status: types.StatusError}, fmt.Errorf("making health request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return types.GetHealthResponse{Status: types.StatusError}, &metrics.UpstreamError{
			Kind: "http_error",
			Code: resp.StatusCode,
			Err:  fmt.Errorf("health endpoint returned status %d", resp.StatusCode),
		}
	}

	return types.GetHealthResponse{Status: types.StatusHealthy}, nil
}

func (w *walletBackendService) configureNetworkClient(network string) *wbclient.Client {
	switch network {
	case types.TESTNET:
		return w.testnetClient
	case types.PUBLIC:
		return w.pubnetClient
	}
	return w.pubnetClient
}

// GetBalancesByAccountAddresses fans out one wbclient.GetAllAccountBalances call
// per unique address using a per-request errgroup bounded by
// maxBalanceConcurrency.
//
//   - Duplicate input addresses collapse to a single result while preserving
//     first-seen order.
//   - The single address-scoped failure is the typed
//     wbclient.ErrAccountNotFound sentinel (accountByAddress:null upstream):
//     it becomes a per-account Error string in the returned
//     []*types.AccountBalances while other accounts in the same request
//     still return their balances.
//   - Every other failure is systemic and returned as a top-level error so
//     the handler emits a 5xx and monitoring sees the outage rather than a
//     200 of per-account error strings. This includes GraphQL errors[]
//     from the server (no structured signal to prove account-locality —
//     schema/query/resolver bugs hit every account the same way), HTTP
//     4xx/5xx, transport failures, signing failures, and request-level
//     cancellation/timeout.
//
// The returned interface{} is a []*types.AccountBalances; the interface type
// is preserved for compatibility with the existing handler signature.
func (w *walletBackendService) GetBalancesByAccountAddresses(ctx context.Context, addresses []string, network string) (_ interface{}, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(w.svcMetrics, walletBackendServiceName, "GetBalancesByAccountAddresses", network, time.Since(start).Seconds(), err)
	}()

	client := w.configureNetworkClient(network)
	if client == nil {
		return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
	}

	unique := utils.DedupePreserveOrder(addresses)

	results := make([]*types.AccountBalances, len(unique))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(w.maxBalanceConcurrency)

	for i, addr := range unique {
		g.Go(func() error {
			balances, fetchErr := client.GetAllAccountBalances(gctx, addr)
			// Always non-nil so the JSON encoder emits "balances": [] even if
			// a future SDK regression returned nil for an account with zero
			// balances. GetAllAccountBalances currently returns a non-nil
			// empty slice; keeping the guard is cheap insurance.
			ab := &types.AccountBalances{
				Address:  addr,
				Balances: []wbtypes.Balance{},
			}
			if fetchErr != nil {
				if !errors.Is(fetchErr, wbclient.ErrAccountNotFound) {
					// Everything except the typed account-not-found sentinel
					// is systemic. wbclient exposes no structured signal that
					// proves a GraphQL errors[] entry or an HTTP 4xx is
					// account-local — most such failures (schema/query bugs,
					// auth/signing, rate limits, transport, ctx cancellation,
					// 5xx) affect every account in the fan-out the same way.
					// Surface them at the top level so monitoring sees the
					// outage instead of a 200 of per-account error strings.
					return classifyWBError(fetchErr)
				}
				logger.ErrorWithContext(gctx, "fetching account balances",
					"address", addr, "error", fetchErr)
				msg := fetchErr.Error()
				ab.Error = &msg
			} else if len(balances) > 0 {
				ab.Balances = balances
			}
			results[i] = ab
			return nil
		})
	}
	if waitErr := g.Wait(); waitErr != nil {
		return nil, waitErr
	}
	return results, nil
}

// classifyWBError wraps a systemic wbclient error with an UpstreamError so
// metrics.ClassifyError can emit a faithful sub-label
// (graphql_error / http_error[:code]). The typed account-not-found case is
// address-scoped and handled at the call site, so it is intentionally not
// matched here. Falls back to substring inspection because wbclient builds
// these errors via fmt.Errorf without %w wrapping.
func classifyWBError(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "GraphQL error:") {
		return &metrics.UpstreamError{Kind: "graphql_error", Err: err}
	}
	if strings.Contains(msg, "unexpected statusCode=") {
		code := 0
		if m := httpStatusCodeRegex.FindStringSubmatch(msg); len(m) == 2 {
			if parsed, parseErr := strconv.Atoi(m[1]); parseErr == nil {
				code = parsed
			}
		}
		return &metrics.UpstreamError{Kind: "http_error", Code: code, Err: err}
	}
	return err
}
