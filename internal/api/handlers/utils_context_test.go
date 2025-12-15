package handlers

import (
	"context"
	"testing"

	"github.com/alitto/pond/v2"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stretchr/testify/assert"

	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func TestFetchCollectible_ContextCancellation(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	pool := pond.NewPool(2)
	_, err := fetchCollectible(mockRPC, ctx, account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "1", "PUBLIC", pool)

	// Should return context.Canceled error
	assert.ErrorIs(t, err, context.Canceled)
}

func TestFetchCollection_ContextCancellation(t *testing.T) {
	mockRPC := &utils.MockRPCService{}
	account := &txnbuild.SimpleAccount{AccountID: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"}
	pool := pond.NewPool(2)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := FetchCollection(mockRPC, ctx, account, "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA", "PUBLIC", pool)

	// Should return context.Canceled error
	assert.ErrorIs(t, err, context.Canceled)
}
