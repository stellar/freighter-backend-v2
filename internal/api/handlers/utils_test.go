package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alitto/pond/v2"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stellar/wallet-backend/pkg/wbclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func TestFetchCollection_Success(t *testing.T) {
	mockRPC := &utils.MockRPCService{}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	pool := pond.NewPool(2)

	collection, err := FetchCollection(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "PUBLIC", pool)

	assert.NoError(t, err)
	assert.NotNil(t, collection)
	assert.Equal(t, "MockNFT", collection.Name)
	assert.Equal(t, "MNFT", collection.Symbol)
}

func TestFetchCollection_InvalidContractID(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	pool := pond.NewPool(2)

	_, err := FetchCollection(mockRPC, context.Background(), account, "INVALID", "PUBLIC", pool)
	assert.Error(t, err)
}

func TestFetchCollection_SimulateInvocationError(t *testing.T) {
	mockRPC := &utils.MockRPCService{
		SimulateError: errors.New("rpc failure"),
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	pool := pond.NewPool(2)

	_, err := FetchCollection(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "PUBLIC", pool)
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

	pool := pond.NewPool(2)
	collectible, err := fetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", tokenId, "PUBLIC", pool)

	assert.NoError(t, err)
	assert.NotNil(t, collectible)

	assert.Equal(t, "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", collectible.Owner)
	assert.Equal(t, server.URL, collectible.TokenUri)
	assert.Equal(t, tokenId, collectible.TokenId)
}

func TestFetchCollectible_InvalidTokenID(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	pool := pond.NewPool(2)
	_, err := fetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "not-a-number", "PUBLIC", pool)
	assert.Error(t, err)
}

func TestFetchCollectible_SimulateInvocationError(t *testing.T) {
	mockRPC := &utils.MockRPCService{
		SimulateError: errors.New("rpc failure"),
	}

	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	pool := pond.NewPool(2)
	_, err := fetchCollectible(mockRPC, context.Background(), account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "1", "PUBLIC", pool)
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

func TestFetchOwnerTokens_NonVecResponse(t *testing.T) {
	// Simulate a contract that returns a non-Vec type (e.g. SCV_VOID)
	nonVecResult := &xdr.ScVal{
		Type: xdr.ScValTypeScvVoid,
	}
	mockRPC := &utils.MockRPCService{
		SimulateResultOverride: nonVecResult,
	}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	owner := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	contractID := "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"

	_, err := fetchOwnerTokens(mockRPC, context.Background(), account, contractID, owner, "PUBLIC")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected SCV_VEC result")
}

func TestIsValidWalletBackendNetwork(t *testing.T) {
	t.Parallel()
	tests := []struct {
		network string
		want    bool
	}{
		{"PUBLIC", true},
		{"TESTNET", true},
		{"FUTURENET", false},
		{"", false},
		{"public", false}, // case-sensitive — matches existing isValidNetwork behavior
	}
	for _, tt := range tests {
		t.Run(tt.network, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isValidWalletBackendNetwork(tt.network))
		})
	}
}

func TestTranslateServiceError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"account not found -> 404", wbclient.ErrAccountNotFound, http.StatusNotFound},
		{"ctx deadline -> 504", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"ctx canceled -> 504", context.Canceled, http.StatusGatewayTimeout},
		{"graphql_error -> 502", &metrics.UpstreamError{Kind: "graphql_error", Err: errors.New("schema bug")}, http.StatusBadGateway},
		{"http_error -> 502", &metrics.UpstreamError{Kind: "http_error", Code: 503, Err: errors.New("upstream down")}, http.StatusBadGateway},
		{"url.Error -> 502", &url.Error{Op: "Post", URL: "http://wb/graphql", Err: errors.New("dial tcp: connection refused")}, http.StatusBadGateway},
		{"net.OpError -> 502", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, http.StatusBadGateway},
		{"generic -> 500", errors.New("anything else"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := translateServiceError(ctx, tt.err, "test resource", "GBTYAFHGNZSTE4VBWZYAGB3SRGJEPTI5I4Y22KZ4JTVAN56LESB6JZOF", "PUBLIC")
			require.NotNil(t, h)
			assert.Equal(t, tt.wantStatus, h.StatusCode)
		})
	}
}
