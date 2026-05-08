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

// GetBalancesByAccountAddresses fans out one wbclient.GetAccountBalances call
// per unique address using a per-request errgroup bounded by
// maxBalanceConcurrency.
//
//   - Duplicate input addresses collapse to a single result while preserving
//     first-seen order.
//   - Address-scoped failures (GraphQL errors, HTTP 4xx) become a per-account
//     Error string in the returned []*types.AccountBalances. Other accounts
//     in the same request still return their balances.
//   - Systemic failures (HTTP 5xx, transport, signing, request-level
//     cancellation/timeout) are returned as a top-level error so the handler
//     emits a 5xx and monitoring sees the outage rather than a 200 of
//     per-account error strings.
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

	// Dedupe while preserving first-seen order.
	seen := make(map[string]struct{}, len(addresses))
	unique := make([]string, 0, len(addresses))
	for _, a := range addresses {
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		unique = append(unique, a)
	}

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
				classified := classifyWBError(fetchErr)
				if !isAddressScopedError(classified) {
					// Surface systemic failures at the top level. Folding them
					// into a per-account Error would mask outages from
					// monitoring (a 200 with N error strings looks healthy).
					return classified
				}
				logger.ErrorWithContext(gctx, "fetching account balances",
					"address", addr, "error", classified)
				msg := classified.Error()
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

// classifyWBError wraps wbclient errors with UpstreamError based on the error
// message shape. wbclient builds errors via fmt.Errorf without %w wrapping,
// so we match on string content. The HTTP status code is parsed out of
// "unexpected statusCode=N" messages so isAddressScopedError can branch on
// 4xx vs 5xx and so the metrics layer gets a faithful sub-label.
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

// isAddressScopedError reports whether err describes a failure specific to a
// single address (so it can be captured in AccountBalances.Error and the
// request can still return 200) rather than a systemic problem that should
// fail the whole request.
//
// Policy:
//
//	context.Canceled / DeadlineExceeded -> systemic (request-wide)
//	Unclassified errors                 -> systemic (likely transport/signing)
//	UpstreamError{Kind:"graphql_error"} -> address-scoped (server-side resolver error scoped to this account)
//	UpstreamError{Kind:"http_error"}    -> systemic (every status — see comment below)
func isAddressScopedError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var upErr *metrics.UpstreamError
	if !errors.As(err, &upErr) {
		return false
	}
	switch upErr.Kind {
	case "graphql_error":
		return true
	case "http_error":
		// wbclient POSTs every call to /graphql/query and translates any
		// HTTP >= 400 into an http_error before parsing the GraphQL body.
		// There is no 4xx code that means "this account doesn't exist" —
		// that signal arrives as a 200 with accountByAddress:null. So every
		// http_error is systemic: wrong base URL (404), bad query body
		// (400/422), auth/signing (401/403), rate limit (429), or an
		// upstream outage (5xx). Failing the whole request lets monitoring
		// see real outages instead of a 200 of per-account error strings.
		return false
	}
	return false
}
