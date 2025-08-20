package utils_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
	"github.com/stretchr/testify/assert"
)

func TestFetchCollection_Success(t *testing.T) {
	mockRPC := &utils.MockRPCService{}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	collection, err := utils.FetchCollection(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA")

	assert.NoError(t, err)
	assert.NotNil(t, collection)
	assert.Equal(t, "MockNFT", collection.Name)
	assert.Equal(t, "MNFT", collection.Symbol)
}

func TestFetchCollection_InvalidContractID(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	_, err := utils.FetchCollection(mockRPC, context.Background(), account, "INVALID")
	assert.Error(t, err)
}

func TestFetchCollection_InvokeContractError(t *testing.T) {
	mockRPC := &utils.MockRPCService{
		SimulateError: errors.New("rpc failure"),
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	_, err := utils.FetchCollection(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA")
	assert.Error(t, err)
}

func TestFetchCollectible_Success(t *testing.T) {
	mockClient := utils.NewMockHTTPClient(utils.MockTokenData)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(utils.MockTokenData)
	}))
	defer server.Close()

	mockRPC := &utils.MockRPCService{
		TokenURIOverride: server.URL,
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	collectible, err := utils.FetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "1", mockClient)

	assert.NoError(t, err)
	assert.NotNil(t, collectible)

	assert.Equal(t, "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", collectible.Owner)
	assert.Equal(t, "MockNFT", collectible.Name)
	assert.Equal(t, server.URL, collectible.TokenUri)
	assert.Equal(t, "https://example.com/image.png", collectible.ImageURL)
	assert.Equal(t, "A mock NFT", collectible.Description)
}

func TestFetchCollectible_InvalidTokenID(t *testing.T) {
	mockClient := utils.NewMockHTTPClient(utils.MockTokenData)
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	_, err := utils.FetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "not-a-number", mockClient)
	assert.Error(t, err)
}

func TestFetchCollectible_InvokeContractError(t *testing.T) {
	mockClient := utils.NewMockHTTPClient(utils.MockTokenData)
	mockRPC := &utils.MockRPCService{
		SimulateError: errors.New("rpc failure"),
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	_, err := utils.FetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "1", mockClient)
	assert.Error(t, err)
}
