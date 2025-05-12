package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errorResponseWriter is a custom ResponseWriter that errors on Write.
type errorResponseWriter struct {
	header http.Header
	code   int
}

func newErrorResponseWriter() *errorResponseWriter {
	return &errorResponseWriter{
		header: make(http.Header),
	}
}

func (w *errorResponseWriter) Header() http.Header {
	return w.header
}

func (w *errorResponseWriter) WriteHeader(statusCode int) {
	w.code = statusCode
}

// Write implements the io.Writer interface and always returns an error.
func (w *errorResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated writer error")
}

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
		w := newErrorResponseWriter() // Use the erroring writer
		err := handler.GetProtocols(w, req)
		require.Error(t, err)
		assert.Equal(t, ErrFailedToEncodeProtocolsToJSONResponse.ClientMessage, err.Error())
	})
}
