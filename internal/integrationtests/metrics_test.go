// ABOUTME: Integration tests for the /metrics Prometheus endpoint.
// ABOUTME: Verifies the endpoint returns valid exposition format and counters increment after API calls.
package integrationtests

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
)

type MetricsTestSuite struct {
	suite.Suite
	freighterContainer *infrastructure.FreighterBackendContainer
	apiURL             string
	metricsURL         string
}

func (s *MetricsTestSuite) SetupSuite() {
	ctx := context.Background()
	var err error
	s.apiURL, err = s.freighterContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	s.Require().NotEmpty(s.apiURL)

	s.metricsURL, err = s.freighterContainer.GetMetricsConnectionString(ctx)
	s.Require().NoError(err)
	s.Require().NotEmpty(s.metricsURL)
}

func (s *MetricsTestSuite) TestMetricsEndpointReturns200() {
	t := s.T()

	resp, err := http.Get(fmt.Sprintf("%s/metrics", s.metricsURL))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	bodyStr := string(body)

	// Verify Prometheus exposition format markers
	require.Contains(t, bodyStr, "# HELP")
	require.Contains(t, bodyStr, "# TYPE")

	// Verify our custom metrics are present
	require.Contains(t, bodyStr, "freighter_http_requests_total")
	require.Contains(t, bodyStr, "freighter_http_request_duration_seconds")
	require.Contains(t, bodyStr, "freighter_http_in_flight_requests")

	// Verify standard collectors
	require.Contains(t, bodyStr, "go_goroutines")
	require.Contains(t, bodyStr, "process_cpu_seconds_total")
}

func (s *MetricsTestSuite) TestMetricsIncrementAfterAPICall() {
	t := s.T()

	// Hit ping endpoint on the public API
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/ping", s.apiURL))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Now check metrics on the internal metrics server
	metricsResp, err := http.Get(fmt.Sprintf("%s/metrics", s.metricsURL))
	require.NoError(t, err)
	defer func() { _ = metricsResp.Body.Close() }()
	require.Equal(t, http.StatusOK, metricsResp.StatusCode)

	body, err := io.ReadAll(metricsResp.Body)
	require.NoError(t, err)
	bodyStr := string(body)

	// Verify the ping request was counted
	found := false
	for _, line := range strings.Split(bodyStr, "\n") {
		if strings.Contains(line, "freighter_http_requests_total") &&
			strings.Contains(line, "/api/v1/ping") &&
			strings.Contains(line, "200") {
			found = true
			break
		}
	}
	require.True(t, found, "expected to find http_requests_total counter for ping endpoint")
}

// TestMetricsNotExposedOnPublicAPI verifies the public API server does not expose /metrics —
// it must only be reachable through the internal metrics port.
func (s *MetricsTestSuite) TestMetricsNotExposedOnPublicAPI() {
	t := s.T()

	resp, err := http.Get(fmt.Sprintf("%s/metrics", s.apiURL))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "/metrics must not be served by the public API server")
}
