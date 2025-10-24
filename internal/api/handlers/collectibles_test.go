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
		collection, err := handler.fetchCollection(ctx, account, contract)
		require.Nil(t, err)
		require.NotNil(t, collection)
		require.Len(t, collection.Collectibles, 2)
	})

	t.Run("returns collection-level error when all token fetches fail", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC, "", "")

		account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
		// Use invalid token IDs (non-numeric) that will fail to parse
		contract := contractDetails{
			ID:       "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
			TokenIDs: []string{"invalid", "bad-token"},
		}

		ctx := context.Background()
		collection, err := handler.fetchCollection(ctx, account, contract)

		// Should return nil collection (collection-level failure)
		require.Nil(t, collection)

		// Should return collection error
		require.NotNil(t, err)
		assert.Equal(t, contract.ID, err.CollectionAddress)
		assert.Contains(t, err.ErrorMessage, "no collectibles fetched")

		// Should include token errors
		require.Len(t, err.Tokens, 2)
		// Check that both token IDs are present (order may vary due to concurrent fetching)
		tokenIDs := []string{err.Tokens[0].TokenID, err.Tokens[1].TokenID}
		assert.Contains(t, tokenIDs, "invalid")
		assert.Contains(t, tokenIDs, "bad-token")
		// Both should have error messages
		assert.NotEmpty(t, err.Tokens[0].ErrorMessage)
		assert.NotEmpty(t, err.Tokens[1].ErrorMessage)
	})

	t.Run("returns collection-level error when token IDs is empty", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC, "", "")

		account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
		contract := contractDetails{
			ID:       "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA",
			TokenIDs: []string{},
		}

		ctx := context.Background()
		collection, err := handler.fetchCollection(ctx, account, contract)

		// Should return nil collection (collection-level failure)
		require.Nil(t, collection)

		// Should return collection error
		require.NotNil(t, err)
		assert.Equal(t, contract.ID, err.CollectionAddress)
		assert.Contains(t, err.ErrorMessage, "no collectibles fetched")

		// Should have empty token errors since no tokens were requested
		assert.Empty(t, err.Tokens)
	})
}

func TestFetchCollectibles(t *testing.T) {
	t.Run("returns empty slice if no collectibles", func(t *testing.T) {
		mockRPC := &utils.MockRPCService{}
		handler := NewCollectiblesHandler(mockRPC, "", "")

		account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
		tokenIDs := []string{}

		ctx := context.Background()
		results, err := handler.fetchCollectibles(ctx, account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", tokenIDs)
		require.Nil(t, err)
		assert.Empty(t, results)
	})
}

func TestFetchMeridianPayCollectibles(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	handler := NewCollectiblesHandler(mockRPC, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "CDSN4MICK7U5XOP4DE6OIZQCRMYO3UTQ5VYZV7ZA7H63OICZPBLXYRGJ")

	ctx := context.Background()
	results, err := handler.fetchMeridianPayCollectibles(ctx, account, account.AccountID)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// When mock returns empty token IDs, should return collection-level errors
	for _, res := range results {
		assert.Nil(t, res.Collection)
		assert.NotNil(t, res.Error)
		assert.Contains(t, res.Error.ErrorMessage, "no collectibles fetched")
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

	// Should have 2 results (Meridian Pay collections, with empty token IDs return errors)
	collections := response.Data.Collections
	require.Len(t, collections, 2)

	for _, col := range collections {
		// Mock returns empty token IDs, so expect collection-level errors
		assert.Nil(t, col.Collection)
		assert.NotNil(t, col.Error)
		assert.Contains(t, col.Error.ErrorMessage, "no collectibles fetched")
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
		// Mock returns empty token IDs, so expect collection-level errors
		assert.Nil(t, col.Collection)
		assert.NotNil(t, col.Error)
		assert.Contains(t, col.Error.ErrorMessage, "no collectibles fetched")
	}
}
