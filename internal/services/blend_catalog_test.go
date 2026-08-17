// ABOUTME: Tests for the Blend catalog service: allowlist loading, catalog
// ABOUTME: mapping, and the earn-options derivation (filtering, grouping, order).
package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	curatedPool   = "CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD"
	uncuratedPool = "CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS"
	frozenPool    = "CFROZENFROZENFROZENFROZENFROZENFROZENFROZENFROZENFROZEN"
)

func writeAllowlist(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "earn-pools.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func status(s wbtypes.BlendPoolStatus) *wbtypes.BlendPoolStatus { return &s }

// poolsFixture: two supply-accepting pools sharing USDC (for grouping and
// ordering), one frozen pool, one not-yet-ingested pool (null status), and
// a disabled reserve — every exclusion rule the derivation must apply.
func poolsFixture() []wbtypes.BlendPool {
	usdc, xlm, name1, name2 := "USDC", "XLM", "Big Pool", "Small Pool"
	return []wbtypes.BlendPool{
		{
			Address:      curatedPool,
			Name:         &name1,
			Status:       status(wbtypes.BlendPoolStatusActive),
			BackstopUsd:  f64(42000),
			BackstopRate: i32(1000000), // 10%
			InRewardZone: true,
			Reserves: []wbtypes.BlendReserve{
				{AssetContractID: "CUSDC", TokenSymbol: &usdc, Enabled: true, SupplyApy: f64(0.043), EmissionsSupplyApr: f64(0.008), SuppliedUsd: f64(1500000)},
				{AssetContractID: "CXLM", TokenSymbol: &xlm, Enabled: false, SupplyApy: f64(0.001), SuppliedUsd: f64(99999)}, // disabled: excluded
			},
		},
		{
			Address: uncuratedPool,
			Name:    &name2,
			Status:  status(wbtypes.BlendPoolStatusOnIce), // on-ice still accepts supply
			Reserves: []wbtypes.BlendReserve{
				{AssetContractID: "CUSDC", TokenSymbol: &usdc, Enabled: true, SupplyApy: f64(0.032), SuppliedUsd: f64(200000)},
			},
		},
		{
			Address: frozenPool,
			Status:  status(wbtypes.BlendPoolStatusFrozen), // rejects deposits: excluded
			Reserves: []wbtypes.BlendReserve{
				{AssetContractID: "CUSDC", TokenSymbol: &usdc, Enabled: true, SupplyApy: f64(9.9), SuppliedUsd: f64(1)},
			},
		},
		{
			Address: "CPENDINGPOOLNOSTATUSYET",
			Status:  nil, // config not ingested: excluded
			Reserves: []wbtypes.BlendReserve{
				{AssetContractID: "CUSDC", TokenSymbol: &usdc, Enabled: true, SupplyApy: f64(0.5), SuppliedUsd: f64(5)},
			},
		},
	}
}

func TestLoadEarnPoolsAllowlist(t *testing.T) {
	t.Run("empty path disables curation", func(t *testing.T) {
		allowlist, err := loadEarnPoolsAllowlist("")
		require.NoError(t, err)
		assert.Nil(t, allowlist)
	})

	t.Run("missing file fails fast", func(t *testing.T) {
		_, err := loadEarnPoolsAllowlist("/nope/earn-pools.json")
		assert.Error(t, err)
	})

	t.Run("malformed json fails fast", func(t *testing.T) {
		_, err := loadEarnPoolsAllowlist(writeAllowlist(t, `{"TESTNET": "not-a-list"}`))
		assert.Error(t, err)
	})

	t.Run("network keys are case-insensitive", func(t *testing.T) {
		allowlist, err := loadEarnPoolsAllowlist(writeAllowlist(t, `{"testnet": ["`+curatedPool+`"]}`))
		require.NoError(t, err)
		assert.True(t, allowlist["TESTNET"][curatedPool])
	})
}

func TestGetEarnOptionsDerivation(t *testing.T) {
	mockWB := &utils.MockWalletBackendService{GetBlendPoolsResult: poolsFixture()}

	t.Run("derives supply-eligible assets with ordered pools", func(t *testing.T) {
		svc, err := NewBlendCatalogService(mockWB, nil, 0, "", nil)
		require.NoError(t, err)

		got, err := svc.GetEarnOptions(context.Background(), types.TESTNET)
		require.NoError(t, err)

		// Only USDC survives: XLM's reserve is disabled, and the frozen and
		// not-yet-ingested pools are excluded entirely.
		require.Len(t, got.Options, 1)
		usdc := got.Options[0]
		assert.Equal(t, "CUSDC", usdc.AssetID)

		// Both supply-accepting pools offer it, larger supplied USD first.
		require.Len(t, usdc.Pools, 2)
		assert.Equal(t, curatedPool, usdc.Pools[0].ID)
		assert.Equal(t, uncuratedPool, usdc.Pools[1].ID)
		require.NotNil(t, usdc.Pools[0].EmissionsSupplyAPR)
		assert.InDelta(t, 0.008, *usdc.Pools[0].EmissionsSupplyAPR, 1e-9)
	})

	t.Run("allowlist filters pools and drops emptied assets", func(t *testing.T) {
		path := writeAllowlist(t, `{"TESTNET": ["`+uncuratedPool+`"]}`)
		svc, err := NewBlendCatalogService(mockWB, nil, 0, path, nil)
		require.NoError(t, err)

		got, err := svc.GetEarnOptions(context.Background(), types.TESTNET)
		require.NoError(t, err)

		require.Len(t, got.Options, 1)
		require.Len(t, got.Options[0].Pools, 1)
		assert.Equal(t, uncuratedPool, got.Options[0].Pools[0].ID)
	})

	t.Run("allowlist for another network does not curate this one", func(t *testing.T) {
		path := writeAllowlist(t, `{"PUBLIC": ["`+curatedPool+`"]}`)
		svc, err := NewBlendCatalogService(mockWB, nil, 0, path, nil)
		require.NoError(t, err)

		got, err := svc.GetEarnOptions(context.Background(), types.TESTNET)
		require.NoError(t, err)
		require.Len(t, got.Options, 1)
		assert.Len(t, got.Options[0].Pools, 2)
	})

	t.Run("unpriced pools sort last with id tie-break", func(t *testing.T) {
		usdc := "USDC"
		svc, err := NewBlendCatalogService(&utils.MockWalletBackendService{
			GetBlendPoolsResult: []wbtypes.BlendPool{
				{Address: "CBBB", Status: status(wbtypes.BlendPoolStatusActive), Reserves: []wbtypes.BlendReserve{{AssetContractID: "CUSDC", TokenSymbol: &usdc, Enabled: true}}},
				{Address: "CAAA", Status: status(wbtypes.BlendPoolStatusActive), Reserves: []wbtypes.BlendReserve{{AssetContractID: "CUSDC", TokenSymbol: &usdc, Enabled: true}}},
				{Address: "CCCC", Status: status(wbtypes.BlendPoolStatusActive), Reserves: []wbtypes.BlendReserve{{AssetContractID: "CUSDC", TokenSymbol: &usdc, Enabled: true, SuppliedUsd: f64(10)}}},
			},
		}, nil, 0, "", nil)
		require.NoError(t, err)

		got, err := svc.GetEarnOptions(context.Background(), types.TESTNET)
		require.NoError(t, err)
		require.Len(t, got.Options, 1)
		pools := got.Options[0].Pools
		require.Len(t, pools, 3)
		assert.Equal(t, "CCCC", pools[0].ID) // priced first
		assert.Equal(t, "CAAA", pools[1].ID) // unpriced, id order
		assert.Equal(t, "CBBB", pools[2].ID)
	})
}

func TestGetPoolsMapping(t *testing.T) {
	svc, err := NewBlendCatalogService(&utils.MockWalletBackendService{GetBlendPoolsResult: poolsFixture()}, nil, 0, "", nil)
	require.NoError(t, err)

	got, err := svc.GetPools(context.Background(), types.TESTNET)
	require.NoError(t, err)

	// The pools catalog is never filtered: all four pools pass through,
	// including frozen and not-yet-ingested ones.
	require.Len(t, got.Pools, 4)
	pool := got.Pools[0]
	assert.Equal(t, curatedPool, pool.ID)
	require.NotNil(t, pool.Status)
	assert.Equal(t, string(wbtypes.BlendPoolStatusActive), *pool.Status)
	require.Len(t, pool.Reserves, 2)
	assert.True(t, pool.Reserves[0].Enabled)
	assert.False(t, pool.Reserves[1].Enabled)
	assert.Nil(t, got.Pools[3].Status)

	require.NotNil(t, pool.BackstopUSD)
	assert.InDelta(t, 42000, *pool.BackstopUSD, 1e-9)
	require.NotNil(t, pool.BackstopRate)
	assert.Equal(t, int32(1000000), *pool.BackstopRate)
	assert.True(t, pool.InRewardZone)
	// A pool the fixture never sets these on: zero-value passthrough, not a
	// mapping bug that silently defaults everyone to true/nonzero.
	assert.False(t, got.Pools[1].InRewardZone)
	assert.Nil(t, got.Pools[1].BackstopUSD)
}

func TestCatalogUpstreamErrors(t *testing.T) {
	upErr := errors.New("wallet-backend down")
	svc, err := NewBlendCatalogService(&utils.MockWalletBackendService{GetBlendPoolsError: upErr}, nil, 0, "", nil)
	require.NoError(t, err)

	_, err = svc.GetPools(context.Background(), types.TESTNET)
	assert.ErrorIs(t, err, upErr)
	// Earn options derive from pools, so they surface the same failure.
	_, err = svc.GetEarnOptions(context.Background(), types.TESTNET)
	assert.ErrorIs(t, err, upErr)
}
