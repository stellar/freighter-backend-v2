package integrationtests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stellar/freighter-backend-v2/internal/integrationtests/infrastructure"
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
	freighterContainer *infrastructure.FreighterBackendContainer
	connectionString   string
}

func (s *ProtocolsTestSuite) SetupSuite() {
	ctx := context.Background()
	var err error
	s.connectionString, err = s.freighterContainer.GetConnectionString(ctx)
	s.Require().NoError(err)
	s.Require().NotEmpty(s.connectionString)
}

func (s *ProtocolsTestSuite) TestGetProtocolsReturns200StatusCodeForValidProtocols() {
	t := s.T()
	ctx := context.Background()

	err := s.freighterContainer.CopyFileToContainer(
		ctx,
		"../../internal/integrationtests/infrastructure/testdata/protocols.json",
		"/app/config/protocols.json",
		0o644,
	)
	require.NoError(t, err)

	resp, err := http.Get(fmt.Sprintf("%s/api/v1/protocols", s.connectionString))
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
	assert.Equal(t, "https://freighter-protocol-icons-dev.stellar.org/protocol-backgrounds/blend.png", protocols[0].BackgroundURL)
	assert.Equal(t, "Blend is a DeFi protocol that allows any entity to create or utilize an immutable lending market that fits its needs.", protocols[0].Description)
	assert.Equal(t, false, protocols[0].IsBlacklisted)
	require.NotNil(t, protocols[0].IsTrending)
	assert.Equal(t, true, *protocols[0].IsTrending)
	assert.Equal(t, "Phoenix", protocols[1].Name)
	assert.Equal(t, "Allbridge Core", protocols[2].Name)

	// Assert on raw JSON to verify omitempty: background_url must be absent
	// entirely for protocols that don't define it.
	type rawResponse struct {
		Data struct {
			Protocols []map[string]any `json:"protocols"`
		} `json:"data"`
	}
	var raw rawResponse
	err = json.Unmarshal(body, &raw)
	require.NoError(t, err)
	_, phoenixHasBackgroundURL := raw.Data.Protocols[1]["background_url"]
	assert.False(t, phoenixHasBackgroundURL, "background_url key should be absent for protocols without a background image")
	_, allbridgeHasBackgroundURL := raw.Data.Protocols[2]["background_url"]
	assert.False(t, allbridgeHasBackgroundURL, "background_url key should be absent for protocols without a background image")
	_, phoenixHasTrending := raw.Data.Protocols[1]["is_trending"]
	assert.False(t, phoenixHasTrending, "is_trending key should be absent for protocols without a trending flag")
	_, allbridgeHasTrending := raw.Data.Protocols[2]["is_trending"]
	assert.False(t, allbridgeHasTrending, "is_trending key should be absent for protocols without a trending flag")
}

func (s *ProtocolsTestSuite) TestGetProtocolsReturns500StatusCodeForInvalidProtocols() {
	t := s.T()
	ctx := context.Background()
	err := s.freighterContainer.CopyFileToContainer(
		ctx,
		"../../internal/integrationtests/infrastructure/testdata/invalid_protocols.json",
		"/app/config/protocols.json",
		0o644,
	)
	require.NoError(t, err)

	resp, err := http.Get(fmt.Sprintf("%s/api/v1/protocols", s.connectionString))
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
