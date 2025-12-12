package services

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/wallet-backend/pkg/wbclient"
	"github.com/stellar/wallet-backend/pkg/wbclient/auth"
)

const (
	walletBackendServiceName = "wallet-backend"
)

type walletBackendService struct {
	pubnetClient  *wbclient.Client
	testnetClient *wbclient.Client
	httpClient    *http.Client
}

func NewWalletBackendService(pubnetUrl, testnetUrl, pubnetSigningKey, testnetSigningKey string) (types.WalletBackendService, error) {
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
	}, nil
}

func (w *walletBackendService) Name() string {
	return walletBackendServiceName
}

func (w *walletBackendService) GetHealth(ctx context.Context, network string) (types.GetHealthResponse, error) {
	// Wallet backend doesn't have a health endpoint, so we just check if the client is configured
	client := w.configureNetworkClient(network)
	if client == nil {
		return types.GetHealthResponse{Status: types.StatusError}, fmt.Errorf("wallet backend client not configured for network: %s", network)
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

func (w *walletBackendService) GetBalancesByAccountAddresses(ctx context.Context, addresses []string, network string) (interface{}, error) {
	client := w.configureNetworkClient(network)
	if client == nil {
		return nil, fmt.Errorf("wallet backend client not configured for network: %s", network)
	}

	balances, err := client.GetBalancesByAccountAddresses(ctx, addresses)
	if err != nil {
		return nil, fmt.Errorf("failed to get balances from wallet backend: %w", err)
	}

	return balances, nil
}
