package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCollectibles(t *testing.T) {
	t.Run("should return collectibles", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewEncoder(w).Encode(utils.MockTokenData); err != nil {
				t.Fatalf("failed to encode mock response: %v", err)
			}
		}))
		defer server.Close()

		mockRPC := &utils.MockRPCService{
			TokenURIOverride: server.URL,
		}

		handler := NewCollectiblesHandler(mockRPC, "", "")

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

		collections := response.Data.Collections
		require.Len(t, collections, 1)

		c0 := collections[0]
		require.NotNil(t, c0.Collection)
		assert.Empty(t, c0.Error)
		assert.Equal(t, 3, len(c0.Collection.Collectibles))
	})
}

func TestFetchCollection(t *testing.T) {

	t.Run("returns collection when collectibles exist", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC, "", "")

		account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
		contract := contractDetails{
			ID:       "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
			TokenIDs: []string{"0", "1"},
		}

		ctx := context.Background()
		collection, err := handler.fetchCollection(ctx, account, contract, "PUBLIC")
		require.Nil(t, err)
		require.NotNil(t, collection)
		require.Len(t, collection.Collectibles, 2)
	})
}

func TestFetchCollectibles(t *testing.T) {
	t.Run("returns empty slice if no collectibles", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC, "", "")

		account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
		tokenIDs := []string{}

		ctx := context.Background()
		results, err := handler.fetchCollectibles(ctx, account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", tokenIDs, "PUBLIC")
		require.Nil(t, err)
		assert.Empty(t, results)
	})
}

func TestFetchMeridianPayCollectibles(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	handler := NewCollectiblesHandler(mockRPC, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "CDSN4MICK7U5XOP4DE6OIZQCRMYO3UTQ5VYZV7ZA7H63OICZPBLXYRGJ")

	ctx := context.Background()
	results, err := handler.fetchMeridianPayCollectibles(ctx, account, account.AccountID, "PUBLIC")
	require.NoError(t, err)
	require.Len(t, results, 2)

	for _, res := range results {
		assert.NotNil(t, res.Collection)
		assert.Empty(t, res.Error)
	}
}

func TestGetCollectibles_WithMeridianPayAddresses(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	handler := NewCollectiblesHandler(mockRPC, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "CDSN4MICK7U5XOP4DE6OIZQCRMYO3UTQ5VYZV7ZA7H63OICZPBLXYRGJ")

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

	// Should dedupe and only have 2 collections
	collections := response.Data.Collections
	require.Len(t, collections, 2)

	for _, col := range collections {
		require.NotNil(t, col.Collection)
		assert.Empty(t, col.Error)
	}
}

func TestGetCollectibles_Empty(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	handler := NewCollectiblesHandler(mockRPC, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "CDSN4MICK7U5XOP4DE6OIZQCRMYO3UTQ5VYZV7ZA7H63OICZPBLXYRGJ")

	body := `{
		"owner": "GB7RQNG6ROYGLFKR3IDAABKI2Y2UAQKEO6BSJVR5IYS7UYQ743O7TOXE",
		"contracts": []
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

	collections := response.Data.Collections
	require.Len(t, collections, 2)

	for _, col := range collections {
		require.NotNil(t, col.Collection)
		assert.Empty(t, col.Error)
	}
}
