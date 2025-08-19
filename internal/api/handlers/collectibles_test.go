package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/utils"
)

type failingWriter struct{}

func (f *failingWriter) Header() http.Header        { return http.Header{} }
func (f *failingWriter) Write([]byte) (int, error)  { return 0, errors.New("write failed") }
func (f *failingWriter) WriteHeader(statusCode int) {}

func TestGetCollectibles(t *testing.T) {
	t.Run("should return collectibles", func(t *testing.T) {
		t.Parallel()
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)
		body := `{
			"owner": "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts": [
				{
					"id": "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
					"token_ids": ["1","2","3"]
				}
			]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()
		err := handler.GetCollectibles(rr, req)
		require.NoError(t, err)

		type expectedResponse struct {
			Data GetCollectiblesPayload `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)
		collectibles := response.Data.Collectibles

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, 3, len(collectibles["CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"]))
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader("{invalid-json}"))
		rr := httptest.NewRecorder()

		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})

	t.Run("missing owner field", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)
		body := `{"contracts":[{"id":"CID123","token_ids":["1"]}]}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})

	t.Run("invalid stellar public key", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)
		body := `{
			"owner": "not-a-stellar-key",
			"contracts":[{"id":"CBIELTK6...AMA","token_ids":["1"]}]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})

	t.Run("invalid contract id", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)
		body := `{
			"owner":"GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts":[{"id":"bad-contract-id","token_ids":["1"]}]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})

	t.Run("rpc service returns error", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{
			SimulateError: errors.New("rpc failure"),
		}
		handler := NewCollectiblesHandler(mockRPC)
		body := `{
			"owner":"GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts":[{"id":"CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA","token_ids":["1"]}]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})

	t.Run("response encoding failure", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)

		body := `{
        "owner":"GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
        "contracts":[{"id":"CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA","token_ids":["1"]}]
    }`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := &failingWriter{}

		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})
}

func TestDecodeCollectibleRequest(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		body := `{
			"owner": "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts": [
				{
					"id": "CID123",
					"token_ids": ["1", "2"]
				}
			]
		}`
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		cr, err := DecodeCollectibleRequest(req)
		assert.NoError(t, err)
		assert.Equal(t, "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE", cr.Owner)
		assert.Len(t, cr.Contracts, 1)
		assert.Equal(t, "CID123", cr.Contracts[0].ID)
		assert.Equal(t, []string{"1", "2"}, cr.Contracts[0].TokenIDs)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "/", strings.NewReader("{invalid-json}"))
		cr, err := DecodeCollectibleRequest(req)
		assert.Nil(t, cr)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JSON")
	})

	t.Run("missing owner", func(t *testing.T) {
		body := `{
			"contracts": [{"id": "CID123", "token_ids": ["1"]}]
		}`
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		cr, err := DecodeCollectibleRequest(req)
		assert.Nil(t, cr)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing or empty key: owner")
	})

	t.Run("empty contracts array", func(t *testing.T) {
		body := `{
			"owner": "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts": []
		}`
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		cr, err := DecodeCollectibleRequest(req)
		assert.Nil(t, cr)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing or empty key: contracts")
	})

	t.Run("empty contract ID", func(t *testing.T) {
		body := `{
			"owner": "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts": [{"id": "", "token_ids": ["1"]}]
		}`
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		cr, err := DecodeCollectibleRequest(req)
		assert.Nil(t, cr)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing or empty key: contracts[0].id")
	})

	t.Run("empty token_ids array", func(t *testing.T) {
		body := `{
			"owner": "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts": [{"id": "CID123", "token_ids": []}]
		}`
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		cr, err := DecodeCollectibleRequest(req)
		assert.Nil(t, cr)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing or empty key: contracts[0].token_ids")
	})
}
