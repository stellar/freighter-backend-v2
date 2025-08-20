package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingWriter struct{}

func (f *failingWriter) Header() http.Header        { return http.Header{} }
func (f *failingWriter) Write([]byte) (int, error)  { return 0, errors.New("write failed") }
func (f *failingWriter) WriteHeader(statusCode int) {}

func TestGetCollectibles(t *testing.T) {
	t.Run("should return collectibles", func(t *testing.T) {
		t.Parallel()

		meta := utils.TokenMetadata{
			Name:        "MockNFT",
			Description: "A mock NFT",
			URL:         "https://example.com/image.png",
			Issuer:      "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(meta)
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			TokenURIOverride: server.URL,
		}

		handler := NewCollectiblesHandler(mockRPC)

		body := `{
			"owner": "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
			"contracts": [
				{
					"id": "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
					"token_ids": ["0","1","2"]
				}
			]
		}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()

		err := handler.GetCollectibles(rr, req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rr.Code)

		type expectedResponse struct {
			Data GetCollectiblesPayload `json:"data"`
		}
		var response expectedResponse
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		collectibles := response.Data.Collectibles
		require.Len(t, collectibles, 1)

		collection := collectibles[0]
		assert.Equal(t, "MockNFT", collection.Name)
		assert.Equal(t, "MNFT", collection.Symbol)
		require.Len(t, collection.Collectibles, 3)

		for _, c := range collection.Collectibles {
			assert.Equal(t, meta.Name, c.Name)
			assert.Equal(t, meta.Description, c.Description)
			assert.Equal(t, meta.URL, c.ImageURL)
			assert.Equal(t, "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", c.Owner)
			assert.Equal(t, server.URL, c.TokenUri)
		}
	})

	// Remaining tests mostly unchanged; they just verify errors
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
		body := `{"owner":"not-a-stellar-key","contracts":[{"id":"CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA","token_ids":["1"]}]}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()
		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})

	t.Run("invalid contract id", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)
		body := `{"owner":"GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE","contracts":[{"id":"bad-contract-id","token_ids":["1"]}]}`
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
		body := `{"owner":"GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE","contracts":[{"id":"CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA","token_ids":["1"]}]}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := httptest.NewRecorder()
		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})

	t.Run("response encoding failure", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)
		body := `{"owner":"GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE","contracts":[{"id":"CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA","token_ids":["1"]}]}`
		req, _ := http.NewRequest("POST", "/api/v1/collectibles", strings.NewReader(body))
		rr := &failingWriter{}
		err := handler.GetCollectibles(rr, req)
		require.Error(t, err)
	})
}
