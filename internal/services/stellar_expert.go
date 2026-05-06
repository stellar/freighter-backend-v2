package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	stellarExpertServiceName = "stellar-expert"
	stellarExpertNetPubnet   = "public"
	stellarExpertNetTestnet  = "testnet"

	stellarExpertHTTPTimeout = 15 * time.Second

	// stellarExpertOrigin matches the SPA's Origin so Cloudflare's Origin
	// gate (returns 402 otherwise) accepts the request. Stellar Expert
	// publishes the API as public; the gate is just an anti-scrape soft
	// rule keyed on this header.
	stellarExpertOrigin = "https://stellar.expert"
)

var (
	// ErrAssetNotFound is returned when Stellar Expert reports the asset is
	// unknown (HTTP 404). Callers should map this to a per-token null in the
	// client response, not a request-wide failure.
	ErrAssetNotFound = errors.New("asset not found in Stellar Expert")

	// ErrAssetMalformed is returned when Stellar Expert rejects the asset id
	// (HTTP 400). Treated like ErrAssetNotFound at the response layer.
	ErrAssetMalformed = errors.New("asset id rejected by Stellar Expert")

	// ErrNetworkNotConfigured indicates we have no base URL for the
	// requested Stellar network.
	ErrNetworkNotConfigured = errors.New("stellar expert URL not configured for network")
)

type stellarExpertService struct {
	pubnetBaseURL  string
	testnetBaseURL string
	apiKey         string
	httpClient     *http.Client
	svcMetrics     *metrics.Service
}

// NewStellarExpertService constructs a thin HTTP client for the Stellar
// Expert /asset endpoint. The base URLs should already include the network
// segment (e.g. https://api.stellar.expert/explorer/public). apiKey, when
// non-empty, is sent as `Authorization: Bearer <apiKey>` on every request.
func NewStellarExpertService(pubnetURL, testnetURL, apiKey string, metricsService *metrics.Service) types.StellarExpertService {
	httpClient := &http.Client{
		Timeout: stellarExpertHTTPTimeout,
		Transport: &http.Transport{
			MaxIdleConns:          200,
			MaxIdleConnsPerHost:   50,
			MaxConnsPerHost:       100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
	return &stellarExpertService{
		pubnetBaseURL:  pubnetURL,
		testnetBaseURL: testnetURL,
		apiKey:         apiKey,
		httpClient:     httpClient,
		svcMetrics:     metricsService,
	}
}

func (s *stellarExpertService) Name() string {
	return stellarExpertServiceName
}

// GetAsset fetches the price snapshot for one asset. assetID must already be
// in Stellar Expert's wire format ("XLM" or "CODE-ISSUER-{1|2}" or a Soroban
// contract id).
func (s *stellarExpertService) GetAsset(ctx context.Context, network, assetID string) (_ *types.StellarExpertAsset, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(s.svcMetrics, stellarExpertServiceName, "GetAsset", network, time.Since(start).Seconds(), err)
	}()

	baseURL, err := s.baseURLForNetwork(network)
	if err != nil {
		return nil, err
	}
	reqURL := fmt.Sprintf("%s/asset/%s", baseURL, assetID)

	req, err := s.newRequest(ctx, reqURL)
	if err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var asset types.StellarExpertAsset
		if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
			return nil, fmt.Errorf("decoding stellar expert response: %w", err)
		}
		return &asset, nil
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, ErrAssetNotFound
	case http.StatusBadRequest:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, ErrAssetMalformed
	default:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, &metrics.UpstreamError{Kind: "http_error", Code: resp.StatusCode, Err: fmt.Errorf("stellar expert status %d", resp.StatusCode)}
	}
}

// GetAssetCandles fetches OHLC candles for one asset over [from, to] at the
// given resolution (seconds). assetID must already be in Stellar Expert wire
// format. Native XLM has no candle data; the upstream returns an empty array
// and this method propagates that as a nil-error empty slice.
func (s *stellarExpertService) GetAssetCandles(ctx context.Context, network, assetID string, from, to time.Time, resolutionSec int) (_ []types.StellarExpertCandle, err error) {
	start := time.Now()
	defer func() {
		metrics.Record(s.svcMetrics, stellarExpertServiceName, "GetAssetCandles", network, time.Since(start).Seconds(), err)
	}()

	baseURL, err := s.baseURLForNetwork(network)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("from", strconv.FormatInt(from.Unix(), 10))
	query.Set("to", strconv.FormatInt(to.Unix(), 10))
	query.Set("resolution", strconv.Itoa(resolutionSec))
	query.Set("order", "asc")
	reqURL := fmt.Sprintf("%s/asset/%s/candles?%s", baseURL, assetID, query.Encode())

	req, err := s.newRequest(ctx, reqURL)
	if err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var candles []types.StellarExpertCandle
		if err := json.NewDecoder(resp.Body).Decode(&candles); err != nil {
			return nil, fmt.Errorf("decoding stellar expert candles response: %w", err)
		}
		return candles, nil
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, ErrAssetNotFound
	case http.StatusBadRequest:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, ErrAssetMalformed
	default:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, &metrics.UpstreamError{Kind: "http_error", Code: resp.StatusCode, Err: fmt.Errorf("stellar expert candles status %d", resp.StatusCode)}
	}
}

func (s *stellarExpertService) newRequest(ctx context.Context, reqURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building stellar expert request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", stellarExpertOrigin)
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	return req, nil
}

func (s *stellarExpertService) baseURLForNetwork(network string) (string, error) {
	switch network {
	case types.PUBLIC:
		if s.pubnetBaseURL == "" {
			return "", fmt.Errorf("%w: %s", ErrNetworkNotConfigured, network)
		}
		return s.pubnetBaseURL, nil
	case types.TESTNET:
		if s.testnetBaseURL == "" {
			return "", fmt.Errorf("%w: %s", ErrNetworkNotConfigured, network)
		}
		return s.testnetBaseURL, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrNetworkNotConfigured, network)
	}
}
