package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
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
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(utils.MockTokenData)
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

		collectibles := response.Data.Collections
		require.Len(t, collectibles, 1)

		collection := collectibles[0]
		assert.Equal(t, "MockNFT", collection.Name)
		assert.Equal(t, "MNFT", collection.Symbol)
		require.Len(t, collection.Collectibles, 3)

		for _, c := range collection.Collectibles {
			assert.Equal(t, "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", c.Owner)
			assert.Equal(t, server.URL, c.TokenUri)
		}
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

func TestFetchCollections(t *testing.T) {
	t.Run("should fetch multiple collections", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)

		account := &txnbuild.SimpleAccount{AccountID: "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE"}
		contracts := []contractDetails{
			{ID: "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", TokenIDs: []string{"0"}},
			{ID: "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", TokenIDs: []string{"1"}},
		}

		ctx := context.Background()
		results, err := handler.fetchCollections(ctx, account, contracts)
		require.NoError(t, err)
		require.Len(t, results, 2)
	})

	t.Run("invalid contract id returns error", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)

		account := &txnbuild.SimpleAccount{AccountID: "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE"}
		contracts := []contractDetails{
			{ID: "not-a-valid-contract", TokenIDs: []string{"1"}},
		}

		ctx := context.Background()
		_, err := handler.fetchCollections(ctx, account, contracts)
		require.Error(t, err)
	})
}

func TestFetchCollection(t *testing.T) {
	t.Run("happy path returns collection", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)

		account := &txnbuild.SimpleAccount{AccountID: "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE"}
		contract := contractDetails{
			ID:       "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
			TokenIDs: []string{"0", "1"},
		}

		ctx := context.Background()
		collection, err := handler.fetchCollection(ctx, account, contract)
		require.NoError(t, err)
		require.Equal(t, contract.ID, collection.CollectionAddress)
		require.Len(t, collection.Collectibles, 2)
	})

	t.Run("rpc failure returns error", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{SimulateError: errors.New("rpc failed")}
		handler := NewCollectiblesHandler(mockRPC)

		account := &txnbuild.SimpleAccount{AccountID: "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE"}
		contract := contractDetails{
			ID:       "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
			TokenIDs: []string{"0"},
		}

		ctx := context.Background()
		_, err := handler.fetchCollection(ctx, account, contract)
		require.Error(t, err)
	})
}

func TestFetchCollectibles(t *testing.T) {
	t.Run("returns all collectibles", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC)

		account := &txnbuild.SimpleAccount{AccountID: "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE"}
		tokenIDs := []string{"0", "1", "2"}

		ctx := context.Background()
		results, err := handler.fetchCollectibles(ctx, account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", tokenIDs)
		require.NoError(t, err)
		require.Len(t, results, 3)
	})

	t.Run("returns error if one collectible fetch fails", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{SimulateError: errors.New("fetch fail")}
		handler := NewCollectiblesHandler(mockRPC)

		account := &txnbuild.SimpleAccount{AccountID: "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE"}
		tokenIDs := []string{"0"}

		ctx := context.Background()
		_, err := handler.fetchCollectibles(ctx, account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", tokenIDs)
		require.Error(t, err)
	})
}
