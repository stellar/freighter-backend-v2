package integrationtests

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/api/handlers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testHTTPError is a local struct for unmarshaling HTTP error responses in tests.
type testHTTPError struct {
	Message       string                 `json:"message"`
	OriginalError json.RawMessage        `json:"originalError,omitempty"`
	StatusCode    int                    `json:"statusCode"`
	Extras        map[string]interface{} `json:"extras,omitempty"`
}

func TestGetProtocols(t *testing.T) {
	t.Run("returns 200 status code for valid protocols", func(t *testing.T) {
		testSuite := NewIntegrationTestSuite(t, &TestConfig{
			TestName:      "protocols-test-returns-200-status-code-for-valid-protocols",
			RunInParallel: true,
			envVars: map[string]string{
				"PROTOCOLS_CONFIG_PATH": "/app/config/protocols.json",
			},
		})
		testSuite.SetupTest()
		defer testSuite.TearDownTest()

		resp, err := http.Get("http://localhost:3002/api/v1/protocols")
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
	})
	t.Run("returns 500 status code for invalid protocols", func(t *testing.T) {
		testSuite := NewIntegrationTestSuite(t, &TestConfig{
			TestName:      "protocols-test-returns-500-status-code-for-invalid-protocols",
			RunInParallel: true,
			envVars: map[string]string{
				"PROTOCOLS_CONFIG_PATH": "/app/config/invalid-protocols.json",
			},
		})
		testSuite.SetupTest()
		defer testSuite.TearDownTest()

		resp, err := http.Get("http://localhost:3002/api/v1/protocols")
		require.NoError(t, err)
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NotNil(t, body)

		var errorResponse testHTTPError
		err = json.Unmarshal(body, &errorResponse)
		require.NoError(t, err)
		require.NotNil(t, errorResponse)
		assert.Equal(t, "An error occurred while fetching protocol configurations.", errorResponse.Message)
	})
}
