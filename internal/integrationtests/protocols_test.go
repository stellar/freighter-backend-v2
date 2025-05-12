package integrationtests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"context"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
)

// testHTTPError is a local struct for unmarshaling HTTP error responses in tests.
type testHTTPError struct {
	Message       string          `json:"message"`
	OriginalError json.RawMessage `json:"originalError,omitempty"`
	StatusCode    int             `json:"statusCode"`
	Extras        map[string]any  `json:"extras,omitempty"`
}

type ProtocolsTestSuite struct {
	suite.Suite
}

func (s *ProtocolsTestSuite) TestGetProtocolsReturns200StatusCodeForValidProtocols() {
	t := s.T()
	ctx := context.Background()

	container := NewFreighterBackendContainer(t, "protocols-test-200-status-code", "protocols-integration-test")
	err := container.CopyFileToContainer(
		ctx,
		"../../internal/integrationtests/infrastructure/testdata/protocols.json",
		"/app/config/protocols.json",
		0644,
	)
	require.NoError(t, err)

	defer func() {
		err = container.Terminate(ctx)
		require.NoError(t, err)
	}()

	connectionString, err := container.GetConnectionString(ctx)
	require.NoError(t, err)
	require.NotNil(t, connectionString)

	resp, err := http.Get(fmt.Sprintf("%s/api/v1/protocols", connectionString))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotNil(t, body)

	type expectedResponse struct {
		Data handlers.GetProtocolsPayload `json:"data"`
	}
	var response expectedResponse
	err = json.Unmarshal(body, &response)
	require.NoError(t, err)
	require.NotNil(t, response)

	protocols := response.Data.Protocols
	require.NotNil(t, protocols)
	require.Equal(t, 3, len(protocols))
	assert.Equal(t, "Blend", protocols[0].Name)
	assert.Equal(t, []string{"Lending", "Borrowing"}, protocols[0].Tags)
	assert.Equal(t, "https://mainnet.blend.capital/", protocols[0].URL)
	assert.Equal(t, "https://freighter-protocol-icons-dev.stellar.org/protocol-icons/blend.svg", protocols[0].IconURL)
	assert.Equal(t, "Blend is a DeFi protocol that allows any entity to create or utilize an immutable lending market that fits its needs.", protocols[0].Description)
	assert.Equal(t, false, protocols[0].IsBlacklisted)
	assert.Equal(t, "Phoenix", protocols[1].Name)
	assert.Equal(t, "Allbridge Core", protocols[2].Name)
}

func (s *ProtocolsTestSuite) TestGetProtocolsReturns500StatusCodeForInvalidProtocols() {
	t := s.T()
	ctx := context.Background()
	container := NewFreighterBackendContainer(t, "protocols-test-500-status-code", "protocols-integration-test")
	err := container.CopyFileToContainer(
		ctx,
		"../../internal/integrationtests/infrastructure/testdata/invalid_protocols.json",
		"/app/config/protocols.json",
		0644,
	)
	require.NoError(t, err)
	defer func() {
		err = container.Terminate(ctx)
		require.NoError(t, err)
	}()

	connectionString, err := container.GetConnectionString(ctx)
	require.NoError(t, err)
	require.NotNil(t, connectionString)

	resp, err := http.Get(fmt.Sprintf("%s/api/v1/protocols", connectionString))
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotNil(t, body)

	var errorResponse testHTTPError
	err = json.Unmarshal(body, &errorResponse)
	require.NoError(t, err)
	require.NotNil(t, errorResponse)
	assert.Equal(t, "An error occurred while processing protocol configurations.", errorResponse.Message)
}
