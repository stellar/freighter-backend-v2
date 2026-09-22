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
	// client response, not a request-wide failure. Straight from upstream it
	// arrives wrapped in a *metrics.UpstreamError, like ErrUpstreamAuth, so
	// the dependency metric labels it http_error:404 — but the history
	// service also returns it BARE from a negative-cache hit, where no HTTP
	// call happened and there is no status to carry. Always match it with
	// errors.Is, never == and never a type assertion on the wrapper.
	ErrAssetNotFound = errors.New("asset not found in Stellar Expert")

	// ErrAssetMalformed is returned when Stellar Expert rejects the asset id
	// (HTTP 400). Treated like ErrAssetNotFound at the response layer, and
	// wrapped the same way — match it with errors.Is.
	//
	// It does NOT have ErrAssetNotFound's bare-from-cache case, for a
	// sharper reason: getAssetMeta stores a 400 as
	// cachedAssetMeta{NotFound: true}, which replays as ErrAssetNotFound.
	// So a malformed id surfaces as ErrAssetMalformed on the cold request
	// and as ErrAssetNotFound for the cached lifetime after it. Every
	// caller today treats the two identically, which is why that is
	// invisible — anything that ever needs to tell them apart must not rely
	// on this error alone.
	ErrAssetMalformed = errors.New("asset id rejected by Stellar Expert")

	// ErrUpstreamAuth is returned when Stellar Expert rejects our
	// CREDENTIALS rather than the asset: 401, 403, or 402 (verified as "no
	// API key", §8.4). It is split out of the transient catch-all because it
	// is categorically different from a blip — a rotated, expired, or
	// misconfigured key fails EVERY request for EVERY asset until someone
	// changes config, so it never self-heals and must never be mistaken for
	// an authoritative answer about an asset.
	//
	// It stays wrapped in a *metrics.UpstreamError, so the service-error
	// metric still labels it http_error:401 / :402 / :403 and the exact code
	// remains visible on the dependency panels.
	ErrUpstreamAuth = errors.New("stellar expert rejected our credentials")

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

// doJSON issues a GET to reqURL and decodes a 200 response body into dest.
// Every non-200 becomes an *UpstreamError so the service-error metric always
// carries the real status rather than "internal"; 404, 400 and the three
// credential codes additionally wrap a sentinel, so callers keep branching on
// errors.Is — 404 → ErrAssetNotFound and 400 → ErrAssetMalformed for
// "unpriceable, do not retry", 401/402/403 → ErrUpstreamAuth. label
// ("asset"/"candles") disambiguates the endpoint in decode/status error
// messages.
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
	case http.StatusNotFound, http.StatusBadRequest:
		// Authoritative answers about this asset, not failures of ours.
		// Wrapped for the same reason the credential case below is: the
		// sentinel still matches through Unwrap, so every caller keeps
		// branching on it, while the metric gets http_error:404 /
		// http_error:400 instead of "internal" — a label this repo reserves
		// for encoding/decoding/validation bugs in our own code.
		//
		// The labelling matters more here than the slander does. These two
		// statuses are the DESIGNED common case for unpriced SEP-41 tokens,
		// so mislabelled they bury freighter_service_errors_total under
		// ordinary traffic — and that series is what
		// FreighterBackendV2StellarExpertDependencyErrors watches, which is
		// the only outage signal /token-stats has left now that it degrades
		// to an empty 200.
		_, _ = io.Copy(io.Discard, resp.Body)
		sentinel := ErrAssetNotFound
		if resp.StatusCode == http.StatusBadRequest {
			sentinel = ErrAssetMalformed
		}
		return &metrics.UpstreamError{
			Kind: "http_error",
			Code: resp.StatusCode,
			Err:  fmt.Errorf("%w: stellar expert %s status %d", sentinel, label, resp.StatusCode),
		}
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden:
		// Our credentials, not this asset. Wrapped so errors.Is finds the
		// sentinel (UpstreamError implements Unwrap) while the metric keeps
		// the precise http_error:<code> label.
		_, _ = io.Copy(io.Discard, resp.Body)
		return &metrics.UpstreamError{
			Kind: "http_error",
			Code: resp.StatusCode,
			Err:  fmt.Errorf("%w: stellar expert %s status %d", ErrUpstreamAuth, label, resp.StatusCode),
		}
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
