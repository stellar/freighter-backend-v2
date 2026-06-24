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

	stellarExpertHTTPTimeout = 15 * time.Second

	// defaultStellarExpertOrigin is the SPA Origin used as a fallback when
	// no explicit origin is configured. Stellar Expert ties API-key quotas
	// to the Origin header; production deployments should override this
	// (typically to a *.freighter.app value associated with the API key).
	defaultStellarExpertOrigin = "https://stellar.expert"
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
	origin         string
	httpClient     *http.Client
	svcMetrics     *metrics.Service
}

// NewStellarExpertService constructs a thin HTTP client for the Stellar
// Expert /asset endpoint. The base URLs should already include the network
// segment (e.g. https://api.stellar.expert/explorer/public). apiKey, when
// non-empty, is sent as `Authorization: Bearer <apiKey>` on every request.
// origin is sent as the Origin header; if empty, defaultStellarExpertOrigin
// is used.
func NewStellarExpertService(pubnetURL, testnetURL, apiKey, origin string, metricsService *metrics.Service) types.StellarExpertService {
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
	if origin == "" {
		origin = defaultStellarExpertOrigin
	}
	return &stellarExpertService{
		pubnetBaseURL:  pubnetURL,
		testnetBaseURL: testnetURL,
		apiKey:         apiKey,
		origin:         origin,
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

	var asset types.StellarExpertAsset
	if err := s.doJSON(ctx, fmt.Sprintf("%s/asset/%s", baseURL, assetID), "asset", &asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

// GetAssetCandles fetches OHLC candles for one asset over [from, to] at the
// given resolution (seconds). assetID must already be in Stellar Expert wire
// format. An empty upstream response (no trades in the window) is propagated
// as a nil-error empty slice; callers then report a null 24h change.
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

	var candles []types.StellarExpertCandle
	if err := s.doJSON(ctx, reqURL, "candles", &candles); err != nil {
		return nil, err
	}
	return candles, nil
}

// doJSON issues a GET to reqURL and decodes a 200 response body into dest. It
// maps 404 → ErrAssetNotFound and 400 → ErrAssetMalformed so callers treat
// unknown/invalid assets as unpriceable without retry, and any other non-200
// to an UpstreamError. label ("asset"/"candles") disambiguates the endpoint in
// decode/status error messages.
func (s *stellarExpertService) doJSON(ctx context.Context, reqURL, label string, dest any) error {
	req, err := s.newRequest(ctx, reqURL)
	if err != nil {
		return err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	defer resp.Body.Close() //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
			return fmt.Errorf("decoding stellar expert %s response: %w", label, err)
		}
		return nil
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrAssetNotFound
	case http.StatusBadRequest:
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrAssetMalformed
	default:
		_, _ = io.Copy(io.Discard, resp.Body)
		return &metrics.UpstreamError{Kind: "http_error", Code: resp.StatusCode, Err: fmt.Errorf("stellar expert %s status %d", label, resp.StatusCode)}
	}
}

func (s *stellarExpertService) newRequest(ctx context.Context, reqURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building stellar expert request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", s.origin)
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
