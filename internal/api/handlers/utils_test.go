package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/go/txnbuild"
	"github.com/stretchr/testify/assert"
)

func TestFetchCollection_Success(t *testing.T) {
	mockRPC := &utils.MockRPCService{}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	collection, err := FetchCollection(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "PUBLIC")

	assert.NoError(t, err)
	assert.NotNil(t, collection)
	assert.Equal(t, "MockNFT", collection.Name)
	assert.Equal(t, "MNFT", collection.Symbol)
}

func TestFetchCollection_InvalidContractID(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	_, err := FetchCollection(mockRPC, context.Background(), account, "INVALID", "PUBLIC")
	assert.Error(t, err)
}

func TestFetchCollection_SimulateInvocationError(t *testing.T) {
	mockRPC := &utils.MockRPCService{
		SimulateError: errors.New("rpc failure"),
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	_, err := FetchCollection(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "PUBLIC")
	assert.Error(t, err)
}

func TestFetchCollectible_Success(t *testing.T) {
	tokenId := "1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(utils.MockTokenData); err != nil {
			t.Fatalf("failed to encode mock response: %v", err)
		}
	}))
	defer server.Close()

	mockRPC := &utils.MockRPCService{
		TokenURIOverride: server.URL,
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	collectible, err := fetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", tokenId, "PUBLIC")

	assert.NoError(t, err)
	assert.NotNil(t, collectible)

	assert.Equal(t, "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", collectible.Owner)
	assert.Equal(t, server.URL, collectible.TokenUri)
	assert.Equal(t, tokenId, collectible.TokenId)
}

func TestFetchCollectible_InvalidTokenID(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	_, err := fetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "not-a-number", "PUBLIC")
	assert.Error(t, err)
}

func TestFetchCollectible_SimulateInvocationError(t *testing.T) {
	mockRPC := &utils.MockRPCService{
		SimulateError: errors.New("rpc failure"),
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	_, err := fetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "1", "PUBLIC")
	assert.Error(t, err)
}

func TestFetchOwnerTokens_Success(t *testing.T) {
	mockRPC := &utils.MockRPCService{}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	owner := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	contractID := "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"

	tokens, err := fetchOwnerTokens(mockRPC, context.Background(), account, contractID, owner, "PUBLIC")
	assert.NoError(t, err)
	assert.Equal(t, []string{}, tokens)
}

func TestFetchOwnerTokens_InvalidContractID(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	owner := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"

	_, err := fetchOwnerTokens(mockRPC, context.Background(), account, "INVALID", owner, "PUBLIC")
	assert.Error(t, err)
}

func TestFetchOwnerTokens_InvalidOwnerAddress(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	contractID := "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"

	_, err := fetchOwnerTokens(mockRPC, context.Background(), account, contractID, "INVALID", "PUBLIC")
	assert.Error(t, err)
}

func TestFetchOwnerTokens_SimulateInvocationError(t *testing.T) {
	mockRPC := &utils.MockRPCService{
		SimulateError: errors.New("rpc failure"),
	}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	owner := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	contractID := "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"

	_, err := fetchOwnerTokens(mockRPC, context.Background(), account, contractID, owner, "PUBLIC")
	assert.Error(t, err)
}

func TestFetchLedgerKeyAccounts_Success(t *testing.T) {
	mockLedgerEntryData := []types.LedgerEntryMap{
		{Account: utils.MockLedgerKeyAccount0 },
	}
	mockRPC := &utils.MockRPCService{
		GetLedgerEntryOverride: mockLedgerEntryData,
	}
	ledgerKeyAccounts, err := FetchLedgerEntries(mockRPC, context.Background(), []string{"GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}, "PUBLIC")
	assert.NoError(t, err)
	assert.Equal(t, mockLedgerEntryData, ledgerKeyAccounts)
}

func TestFetchLedgerKeyAccounts_GetLedgerEntryError(t *testing.T) {
	mockRPC := &utils.MockRPCService{
		GetLedgerEntryError: errors.New("rpc failure"),
	}
	_, err := FetchLedgerEntries(mockRPC, context.Background(), []string{"GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}, "PUBLIC")
	assert.Error(t, err)
}