package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func TestGetProtocols(t *testing.T) {
	t.Run("should return protocols", func(t *testing.T) {
		t.Parallel()
		handler := NewProtocolsHandler("testdata/protocols.json")
		req, _ := http.NewRequest("GET", "/api/v1/protocols", nil)
		rr := httptest.NewRecorder()
		err := handler.GetProtocols(rr, req)
		require.NoError(t, err)

		type expectedResponse struct {
			Data GetProtocolsPayload `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		protocols := response.Data.Protocols

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, 3, len(protocols))
		assert.Equal(t, "Blend", protocols[0].Name)
		assert.Equal(t, []string{"Lending", "Borrowing"}, protocols[0].Tags)
		assert.Equal(t, "https://mainnet.blend.capital/", protocols[0].URL)
		assert.Equal(t, "https://freighter-protocol-icons-dev.stellar.org/protocol-icons/blend.svg", protocols[0].IconURL)
		assert.Equal(t, "https://freighter-protocol-icons-dev.stellar.org/protocol-backgrounds/blend.png", protocols[0].BackgroundURL)
		assert.Equal(t, "Blend is a DeFi protocol that allows any entity to create or utilize an immutable lending market that fits its needs.", protocols[0].Description)
		assert.Equal(t, false, protocols[0].IsBlacklisted)
		assert.Equal(t, true, protocols[0].IsWalletConnectNotSupported)
		assert.Equal(t, "Phoenix", protocols[1].Name)
		assert.Equal(t, true, protocols[1].IsBlacklisted)
		assert.Equal(t, false, protocols[1].IsWalletConnectNotSupported)
		assert.Equal(t, "Allbridge Core", protocols[2].Name)

		// Assert on raw JSON to verify omitempty: the key must be absent entirely
		// for protocols that don't define a background_url.
		type rawResponse struct {
			Data struct {
				Protocols []map[string]any `json:"protocols"`
			} `json:"data"`
		}
		var raw rawResponse
		err = json.Unmarshal(rr.Body.Bytes(), &raw)
		require.NoError(t, err)
		_, hasBackgroundURL := raw.Data.Protocols[2]["background_url"]
		assert.False(t, hasBackgroundURL, "background_url key should be absent for protocols without a background image")
	})
	t.Run("should return error if protocols file is not found", func(t *testing.T) {
		t.Parallel()
		handler := NewProtocolsHandler("testdata/non_existent_file.json")
		req, _ := http.NewRequest("GET", "/api/v1/protocols", nil)
		rr := httptest.NewRecorder()
		err := handler.GetProtocols(rr, req)
		require.Error(t, err)
		assert.Equal(t, ErrFailedToReadProtocolsConfig.ClientMessage, err.Error())
	})
	t.Run("should return error if protocols file is invalid", func(t *testing.T) {
		t.Parallel()
		handler := NewProtocolsHandler("testdata/invalid_protocols.json")
		req, _ := http.NewRequest("GET", "/api/v1/protocols", nil)
		rr := httptest.NewRecorder()
		err := handler.GetProtocols(rr, req)
		require.Error(t, err)
		assert.Equal(t, ErrFailedToUnmarshalProtocolsConfig.ClientMessage, err.Error())
	})
	t.Run("should return error on encoding failure", func(t *testing.T) {
		t.Parallel()
		handler := NewProtocolsHandler("testdata/protocols.json") // Use valid data file
		req, _ := http.NewRequest("GET", "/api/v1/protocols", nil)
		w := utils.NewErrorResponseWriter(true)
		err := handler.GetProtocols(w, req)
		require.Error(t, err)
		assert.Equal(t, ErrFailedToEncodeProtocolsToJSONResponse.ClientMessage, err.Error())
	})
}
