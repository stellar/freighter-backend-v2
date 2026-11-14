// ABOUTME: HTTP client for the Cross Mesh Ingress API: chains, deposit addresses, deposits and settlements.
// ABOUTME: Each Stellar network maps to one Cross Mesh environment with its own base URL and bearer key.
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	crossMeshServiceName = "crossmesh"
	// crossMeshTimeout bounds every upstream call.
	crossMeshTimeout = 10 * time.Second
	// Freighter only issues standard-speed addresses that deliver USDC.
	crossMeshDeliveryAsset = "USDC"
	crossMeshSpeed         = "standard"
)

// CrossMeshError is a non-200 answer from Cross Mesh, decoded from its
// {"error":{"code","message","request_id"}} envelope. Code and RequestID are
// empty when the body was not that envelope. Match it with errors.As: it
// arrives wrapped in a *metrics.UpstreamError so the service-error metric
// carries the HTTP status.
type CrossMeshError struct {
	Status    int
	Code      string
	RequestID string
}

func (e *CrossMeshError) Error() string {
	return fmt.Sprintf("crossmesh status %d code %q request_id %q", e.Status, e.Code, e.RequestID)
}

type crossMeshService struct {
	cfg        config.CrossMeshConfig
	httpClient *http.Client
	svcMetrics *metrics.Service
	timeout    time.Duration
}

// NewCrossMeshService builds the Cross Mesh client. metricsService may be nil.
func NewCrossMeshService(cfg config.CrossMeshConfig, metricsService *metrics.Service) types.CrossMeshService {
	return &crossMeshService{
		cfg:        cfg,
		httpClient: utils.NewUpstreamHTTPClient(),
		svcMetrics: metricsService,
		timeout:    crossMeshTimeout,
	}
}

func (s *crossMeshService) GetChains(ctx context.Context, network string) (*types.CrossMeshChains, error) {
	return crossMeshCall[types.CrossMeshChains](ctx, s, "GetChains", network, http.MethodGet, "/v1/chains", nil)
}

func (s *crossMeshService) LookupDepositAddress(ctx context.Context, network, recipient string) (*types.CrossMeshDepositAddress, error) {
	query := url.Values{}
	query.Set("recipient", recipient)
	query.Set("delivery_asset", crossMeshDeliveryAsset)
	query.Set("speed", crossMeshSpeed)
	page, err := crossMeshCall[types.CrossMeshDepositAddressPage](ctx, s, "LookupDepositAddress", network, http.MethodGet, "/v1/deposit-addresses?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// The filters select at most one row; none means no address yet.
	if len(page.Data) == 0 {
		return nil, nil
	}
	return &page.Data[0], nil
}

func (s *crossMeshService) CreateDepositAddress(ctx context.Context, network, recipient string) (*types.CrossMeshDepositAddress, error) {
	body := map[string]string{"recipient": recipient, "delivery_asset": crossMeshDeliveryAsset, "speed": crossMeshSpeed}
	return crossMeshCall[types.CrossMeshDepositAddress](ctx, s, "CreateDepositAddress", network, http.MethodPost, "/v1/deposit-addresses", body)
}

func (s *crossMeshService) ListDeposits(ctx context.Context, network, recipient, cursor string, limit int) (*types.CrossMeshDepositPage, error) {
	query := url.Values{}
	query.Set("recipient", recipient)
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("starting_after", cursor)
	}
	return crossMeshCall[types.CrossMeshDepositPage](ctx, s, "ListDeposits", network, http.MethodGet, "/v1/deposits?"+query.Encode(), nil)
}

func (s *crossMeshService) GetSettlement(ctx context.Context, network, id string) (*types.CrossMeshSettlement, error) {
	return crossMeshCall[types.CrossMeshSettlement](ctx, s, "GetSettlement", network, http.MethodGet, "/v1/settlements/"+url.PathEscape(id), nil)
}

// recordCall records one call's count, duration and, on failure, its error
// type into the service metrics. Every error counts: unlike wallet-backend's
// account-not-found, no Cross Mesh outcome is a normal client result here.
// metrics.Record is nil-safe and buckets unknown networks.
func (s *crossMeshService) recordCall(method, network string, start time.Time, err error) {
	metrics.Record(s.svcMetrics, crossMeshServiceName, method, network, time.Since(start).Seconds(), err)
}

// crossMeshCall sends one request (body, when non-nil, as JSON), decodes a 200
// into T, and records the call metrics under method.
func crossMeshCall[T any](ctx context.Context, s *crossMeshService, method, network, httpMethod, path string, body any) (_ *T, err error) {
	start := time.Now()
	defer func() { s.recordCall(method, network, start, err) }()

	baseURL, apiKey := crossMeshEndpoint(s.cfg, network)
	if baseURL == "" {
		return nil, fmt.Errorf("crossmesh: no environment for network %q", network)
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var reqBody io.Reader
	if body != nil {
		encoded, encErr := json.Marshal(body)
		if encErr != nil {
			return nil, fmt.Errorf("encoding crossmesh request: %w", encErr)
		}
		reqBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, httpMethod, baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("building crossmesh request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crossmesh %s: %w", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		var envelope struct {
			Error struct {
				Code      string `json:"code"`
				RequestID string `json:"request_id"`
			} `json:"error"`
		}
		// A body that is not the envelope leaves Code and RequestID empty.
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		return nil, &metrics.UpstreamError{
			Kind: "http_error",
			Code: resp.StatusCode,
			Err:  &CrossMeshError{Status: resp.StatusCode, Code: envelope.Error.Code, RequestID: envelope.Error.RequestID},
		}
	}

	var out T
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding crossmesh %s response: %w", method, err)
	}
	return &out, nil
}
