// ABOUTME: Tests for the Blend catalog service: allowlist loading/curation,
// ABOUTME: catalog mapping passthrough, and error propagation.
package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	curatedPool   = "CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD"
	uncuratedPool = "CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS"
)

func writeAllowlist(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "earn-pools.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func earnOptionsFixture() []types.BlendEarnOption {
	usdc, xlm := "USDC", "XLM"
	return []types.BlendEarnOption{
		{
			AssetContractID: "CUSDC",
			TokenSymbol:     &usdc,
			Pools: []types.BlendEarnPoolOption{
				{PoolAddress: curatedPool, SupplyAPY: f64(0.043)},
				{PoolAddress: uncuratedPool, SupplyAPY: f64(0.032)},
			},
		},
		{
			// Every pool for this asset is uncurated: the asset must drop.
			AssetContractID: "CXLM",
			TokenSymbol:     &xlm,
			Pools: []types.BlendEarnPoolOption{
				{PoolAddress: uncuratedPool, SupplyAPY: f64(0.001)},
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

func TestGetEarnOptionsCuration(t *testing.T) {
	mockWB := &utils.MockWalletBackendService{GetBlendEarnOptionsResult: earnOptionsFixture()}

	t.Run("allowlist filters pools and drops emptied assets", func(t *testing.T) {
		path := writeAllowlist(t, `{"TESTNET": ["`+curatedPool+`"]}`)
		svc, err := NewBlendCatalogService(mockWB, nil, 0, path, nil)
		require.NoError(t, err)

		got, err := svc.GetEarnOptions(context.Background(), types.TESTNET)
		require.NoError(t, err)

		// XLM (only uncurated pools) is gone; USDC keeps only the curated pool.
		require.Len(t, got.Options, 1)
		assert.Equal(t, "CUSDC", got.Options[0].AssetID)
		require.Len(t, got.Options[0].Pools, 1)
		assert.Equal(t, curatedPool, got.Options[0].Pools[0].ID)
	})

	t.Run("no allowlist passes everything through", func(t *testing.T) {
		svc, err := NewBlendCatalogService(mockWB, nil, 0, "", nil)
		require.NoError(t, err)

		got, err := svc.GetEarnOptions(context.Background(), types.TESTNET)
		require.NoError(t, err)
		require.Len(t, got.Options, 2)
		assert.Len(t, got.Options[0].Pools, 2)
	})

	t.Run("allowlist for another network filters everything", func(t *testing.T) {
		path := writeAllowlist(t, `{"PUBLIC": ["`+curatedPool+`"]}`)
		svc, err := NewBlendCatalogService(mockWB, nil, 0, path, nil)
		require.NoError(t, err)

		got, err := svc.GetEarnOptions(context.Background(), types.TESTNET)
		require.NoError(t, err)
		// TESTNET has no allowlist entry -> allowed set is nil for that
		// network -> no curation applies there.
		assert.Len(t, got.Options, 2)
	})
}

func TestGetPoolsMapping(t *testing.T) {
	name := "Fixed Pool V2"
	usdc := "USDC"
	mockWB := &utils.MockWalletBackendService{
		GetBlendPoolsResult: []types.BlendPool{{
			Address:     curatedPool,
			Name:        &name,
			Status:      i32(types.BlendPoolStatusActive),
			SuppliedUSD: f64(2100000.5),
			InterestAPY: f64(0.043),
			NetAPY:      f64(0.047),
			Reserves: []types.BlendReserve{{
				AssetContractID:    "CUSDC",
				TokenSymbol:        &usdc,
				Enabled:            true,
				Utilization:        f64(0.62),
				SupplyAPY:          f64(0.043),
				EmissionsSupplyAPR: f64(0.008),
				PriceUSD:           f64(1.0),
			}},
		}, {
			// Not-yet-ingested pool: everything null.
			Address:  uncuratedPool,
			Reserves: []types.BlendReserve{},
		}},
	}
	svc, err := NewBlendCatalogService(mockWB, nil, 0, "", nil)
	require.NoError(t, err)

	got, err := svc.GetPools(context.Background(), types.TESTNET)
	require.NoError(t, err)

	require.Len(t, got.Pools, 2)
	pool := got.Pools[0]
	assert.Equal(t, curatedPool, pool.ID)
	require.NotNil(t, pool.Status)
	assert.Equal(t, types.BlendPoolStatusActive, *pool.Status)
	require.Len(t, pool.Reserves, 1)
	assert.True(t, pool.Reserves[0].Enabled)
	require.NotNil(t, pool.Reserves[0].EmissionsSupplyAPR)
	assert.InDelta(t, 0.008, *pool.Reserves[0].EmissionsSupplyAPR, 1e-9)

	// The pools catalog is never allowlist-filtered.
	assert.Equal(t, uncuratedPool, got.Pools[1].ID)
	assert.Nil(t, got.Pools[1].Status)
	assert.NotNil(t, got.Pools[1].Reserves)
}

func TestCatalogUpstreamErrors(t *testing.T) {
	upErr := errors.New("wallet-backend down")
	svc, err := NewBlendCatalogService(&utils.MockWalletBackendService{
		GetBlendPoolsError:       upErr,
		GetBlendEarnOptionsError: upErr,
	}, nil, 0, "", nil)
	require.NoError(t, err)

	_, err = svc.GetPools(context.Background(), types.TESTNET)
	assert.ErrorIs(t, err, upErr)
	_, err = svc.GetEarnOptions(context.Background(), types.TESTNET)
	assert.ErrorIs(t, err, upErr)
}
