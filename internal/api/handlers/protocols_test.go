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
		assert.Equal(t, "Blend is a DeFi protocol that allows any entity to create or utilize an immutable lending market that fits its needs.", protocols[0].Description)
		assert.Equal(t, false, protocols[0].IsBlacklisted)
		assert.Equal(t, "Phoenix", protocols[1].Name)
		assert.Equal(t, "Allbridge Core", protocols[2].Name)
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
		w := utils.NewErrorResponseWriter(true) // Use shared ErrorResponseWriter configured to fail write
		err := handler.GetProtocols(w, req)
		require.Error(t, err)
		assert.Equal(t, ErrFailedToEncodeProtocolsToJSONResponse.ClientMessage, err.Error())
	})
}
