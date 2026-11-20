// ABOUTME: Tests for the hand-written service mocks.
// ABOUTME: Checks that MockCrossMeshService satisfies the interface and delegates each call to its func.
package utils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

func TestMockCrossMeshService_Delegates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := &types.CrossMeshDepositAddress{DepositAddress: "0xabc"}
	var svc types.CrossMeshService = &MockCrossMeshService{
		GetChainsFunc: func(_ context.Context, network string) (*types.CrossMeshChains, error) {
			return &types.CrossMeshChains{Chains: []types.CrossMeshChain{{Chain: network}}}, nil
		},
		LookupDepositAddressFunc: func(context.Context, string, string) (*types.CrossMeshDepositAddress, error) { return addr, nil },
		CreateDepositAddressFunc: func(context.Context, string, string) (*types.CrossMeshDepositAddress, error) { return addr, nil },
		ListDepositsFunc: func(_ context.Context, _, _, cursor string, _ int) (*types.CrossMeshDepositPage, error) {
			return &types.CrossMeshDepositPage{NextCursor: &cursor}, nil
		},
		GetSettlementFunc: func(_ context.Context, _, id string) (*types.CrossMeshSettlement, error) {
			return &types.CrossMeshSettlement{SettlementID: id}, nil
		},
	}

	chains, err := svc.GetChains(ctx, types.TESTNET)
	require.NoError(t, err)
	assert.Equal(t, types.TESTNET, chains.Chains[0].Chain)

	got, err := svc.LookupDepositAddress(ctx, types.TESTNET, "G")
	require.NoError(t, err)
	assert.Same(t, addr, got)

	got, err = svc.CreateDepositAddress(ctx, types.TESTNET, "G")
	require.NoError(t, err)
	assert.Same(t, addr, got)

	page, err := svc.ListDeposits(ctx, types.TESTNET, "G", "cur", 100)
	require.NoError(t, err)
	assert.Equal(t, "cur", *page.NextCursor)

	stl, err := svc.GetSettlement(ctx, types.TESTNET, "stl_1")
	require.NoError(t, err)
	assert.Equal(t, "stl_1", stl.SettlementID)
}
