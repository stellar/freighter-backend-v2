package services

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/stellar/wallet-backend/pkg/wbclient"
	"github.com/stellar/wallet-backend/pkg/wbclient/auth"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	walletBackendServiceName = "wallet-backend"
)

type walletBackendService struct {
	pubnetClient  *wbclient.Client
	testnetClient *wbclient.Client
	httpClient    *http.Client
	svcMetrics    *metrics.Service
}

func NewWalletBackendService(pubnetUrl, testnetUrl, pubnetSigningKey, testnetSigningKey string, m *metrics.Service) (types.WalletBackendService, error) {
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
		pubnetClient:  pubnetClient,
		testnetClient: testnetClient,
		httpClient:    httpClient,
		svcMetrics:    m,
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

func (w *walletBackendService) GetBalancesByAccountAddresses(ctx context.Context, addresses []string, network string) (_ interface{}, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(w.svcMetrics, walletBackendServiceName, "GetBalancesByAccountAddresses", network, time.Since(start).Seconds(), err)
	}()

	client := w.configureNetworkClient(network)
	if client == nil {
		return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
	}

	balances, err := client.GetBalancesByAccountAddresses(ctx, addresses)
	if err != nil {
		return nil, fmt.Errorf("failed to get balances from wallet backend: %w", classifyWBError(err))
	}

	return balances, nil
}

// classifyWBError wraps wbclient errors with UpstreamError based on error message patterns.
// wbclient uses fmt.Sprintf (not %w), so we match on string content.
func classifyWBError(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "GraphQL error:") {
		return &metrics.UpstreamError{Kind: "graphql_error", Err: err}
	}
	if strings.Contains(msg, "unexpected statusCode=") {
		return &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	return err
}
