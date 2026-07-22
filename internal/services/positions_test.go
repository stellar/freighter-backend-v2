// ABOUTME: Tests for the positions service mapper: row filtering, earnings
// ABOUTME: conversion, null propagation, and the account-level aggregate.
package services

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

func f64(v float64) *float64 { return &v }
func str(s string) *string   { return &s }
func i32(v int32) *int32     { return &v }

// reserveFixture mirrors the shape observed on the live testnet dev instance
// (user account GDW6QB3B...): XLM held entirely as collateral with real
// earned interest, USDC as collateral, a dust wBTC borrow, and a fully-exited
// wETH row that must not become a display row.
func reserveFixture() []types.BlendReservePosition {
	return []types.BlendReservePosition{
		{
			AssetContractID:     "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
			TokenSymbol:         nil, // XLM SAC missing from the registry, observed live
			TokenDecimals:       i32(7),
			SuppliedTokens:      "0",
			CollateralTokens:    "67125489343",
			BorrowedTokens:      "0",
			SuppliedUSD:         f64(2819.270552406),
			SupplyAPY:           f64(3.240617830176194),
			EmissionsAPR:        f64(0),
			InterestEarned:      "2125489343",
			EmissionsEarnedBLND: "0",
			PriceUSD:            f64(0.42),
		},
		{
			AssetContractID:     "CCYM3TPDGQODFOC2OQDND6C7SKHO3TWD37CYN35I6K66JO5X3SUANEHN",
			TokenSymbol:         str("USDC"),
			TokenDecimals:       i32(7),
			SuppliedTokens:      "0",
			CollateralTokens:    "8000168408",
			BorrowedTokens:      "0",
			SuppliedUSD:         f64(800.0168408),
			SupplyAPY:           f64(0.00104137909709201),
			InterestEarned:      "168408",
			EmissionsEarnedBLND: "0",
			PriceUSD:            f64(1),
		},
		{
			AssetContractID:     "CBWBTCWBTCWBTCWBTCWBTCWBTCWBTCWBTCWBTCWBTCWBTCWBTCWBTC1",
			TokenSymbol:         str("wBTC"),
			TokenDecimals:       i32(7),
			SuppliedTokens:      "0",
			CollateralTokens:    "0",
			BorrowedTokens:      "2",
			BorrowedUSD:         f64(0.02),
			BorrowAPY:           f64(4.72998834498228),
			InterestEarned:      "0",
			EmissionsEarnedBLND: "0",
			PriceUSD:            f64(100000),
		},
		{
			// Fully-exited row: upstream emits it for earnings history; it
			// must not appear in either display list.
			AssetContractID:     "CBWETHWETHWETHWETHWETHWETHWETHWETHWETHWETHWETHWETHWETH1",
			TokenSymbol:         str("wETH"),
			TokenDecimals:       i32(7),
			SuppliedTokens:      "0",
			CollateralTokens:    "0",
			BorrowedTokens:      "0",
			SuppliedUSD:         f64(0),
			InterestEarned:      "0",
			EmissionsEarnedBLND: "0",
			PriceUSD:            f64(4000),
		},
	}
}

func TestMapBlendDetailRows(t *testing.T) {
	detail := mapBlendDetail(reserveFixture())

	// wETH's all-zero row is filtered; XLM and USDC become supply rows.
	require.Len(t, detail.Supply, 2)
	require.Len(t, detail.Borrow, 1)

	xlm := detail.Supply[0]
	assert.Nil(t, xlm.Symbol) // registry gap passes through; client truncates asset_id
	assert.Equal(t, "0", xlm.SuppliedTokens)
	assert.Equal(t, "67125489343", xlm.CollateralTokens)
	assert.Equal(t, "67125489343", xlm.TotalTokens)

	// interest_earned_usd = raw / 10^decimals × price:
	// 2125489343 / 1e7 × 0.42 = 89.27 (the live account's real figure).
	require.NotNil(t, xlm.InterestEarnedUSD)
	assert.InDelta(t, 89.2705524, *xlm.InterestEarnedUSD, 1e-4)

	usdc := detail.Supply[1]
	require.NotNil(t, usdc.InterestEarnedUSD)
	assert.InDelta(t, 0.0168408, *usdc.InterestEarnedUSD, 1e-9)

	wbtc := detail.Borrow[0]
	assert.Equal(t, "2", wbtc.BorrowedTokens)
	require.NotNil(t, wbtc.USDValue)
	assert.InDelta(t, 0.02, *wbtc.USDValue, 1e-9)
}

func TestMapBlendDetailNullSafety(t *testing.T) {
	rows := []types.BlendReservePosition{{
		AssetContractID:     "CUNPRICED",
		TokenDecimals:       nil, // no registry entry
		SuppliedTokens:      "100",
		CollateralTokens:    "0",
		BorrowedTokens:      "0",
		SuppliedUSD:         nil, // no oracle price
		InterestEarned:      "50",
		EmissionsEarnedBLND: "0",
		PriceUSD:            nil,
	}}
	detail := mapBlendDetail(rows)

	require.Len(t, detail.Supply, 1)
	row := detail.Supply[0]
	assert.Nil(t, row.USDValue)
	// Missing decimals/price make the USD conversion unavailable, not zero.
	assert.Nil(t, row.InterestEarnedUSD)
	// The raw token figures still pass through.
	assert.Equal(t, "50", row.InterestEarned)
	assert.Equal(t, "100", row.TotalTokens)
}

func TestAccountAggregate(t *testing.T) {
	pool := func(usd, apy *float64) types.BlendPoolPosition {
		return types.BlendPoolPosition{USDValue: usd, NetAPY: apy}
	}

	t.Run("weighted mean across pools", func(t *testing.T) {
		total, apy := accountAggregate([]types.BlendPoolPosition{
			pool(f64(9000), f64(0.05)),
			pool(f64(1000), f64(0.01)),
		})
		require.NotNil(t, total)
		assert.InDelta(t, 10000, *total, 1e-9)
		require.NotNil(t, apy)
		assert.InDelta(t, 0.046, *apy, 1e-9) // (9000×5% + 1000×1%) / 10000
	})

	t.Run("strict null: one unpriced pool nulls the header", func(t *testing.T) {
		total, apy := accountAggregate([]types.BlendPoolPosition{
			pool(f64(9000), f64(0.05)),
			pool(nil, nil),
		})
		assert.Nil(t, total)
		assert.Nil(t, apy)
	})

	t.Run("null netApy nulls the rate but keeps the total", func(t *testing.T) {
		total, apy := accountAggregate([]types.BlendPoolPosition{
			pool(f64(9000), f64(0.05)),
			pool(f64(1000), nil),
		})
		require.NotNil(t, total)
		assert.InDelta(t, 10000, *total, 1e-9)
		assert.Nil(t, apy)
	})

	t.Run("no positions is a genuine zero, apy null", func(t *testing.T) {
		total, apy := accountAggregate(nil)
		require.NotNil(t, total)
		assert.Equal(t, 0.0, *total)
		assert.Nil(t, apy)
	})

	t.Run("zero net base yields null apy, zero total", func(t *testing.T) {
		total, apy := accountAggregate([]types.BlendPoolPosition{
			pool(f64(0), f64(0.05)),
		})
		require.NotNil(t, total)
		assert.Equal(t, 0.0, *total)
		assert.Nil(t, apy)
	})
}

func TestGetAccountPositionsMapsAndPassesThrough(t *testing.T) {
	name := "TestnetV2"
	mockWB := &utils.MockWalletBackendService{
		GetBlendPositionsResult: &types.BlendAccountPositions{
			Pools: []types.BlendPoolPosition{{
				PoolAddress: "CCEBVDYMCCECIVWVOJSKUNLTVDIRLTRUCVZDVLKXKQZWSCF3DVQGJVIX",
				PoolName:    &name,
				USDValue:    f64(3619.267393206),
				SuppliedUSD: f64(3619.287393206),
				BorrowedUSD: f64(0.02),
				NetAPY:      f64(2.5245032003462415),
				Reserves:    reserveFixture(),
			}},
		},
	}
	svc := NewPositionsService(mockWB, nil, 0, nil)

	got, err := svc.GetAccountPositions(context.Background(), "GDW6QB3BFPQ3I4LH752JD2HYADFM2T4RVRCEUNCCH7MICWZR67NL5552", types.TESTNET)
	require.NoError(t, err)

	require.Len(t, got.Positions, 1)
	row := got.Positions[0]
	assert.Equal(t, "blend", row.Protocol)
	assert.Equal(t, "CCEBVDYMCCECIVWVOJSKUNLTVDIRLTRUCVZDVLKXKQZWSCF3DVQGJVIX", row.ID)
	require.NotNil(t, row.Name)
	assert.Equal(t, "TestnetV2", *row.Name)
	require.NotNil(t, row.NetUSD)
	assert.InDelta(t, 3619.267393206, *row.NetUSD, 1e-9)
	require.NotNil(t, row.Blend)
	assert.Len(t, row.Blend.Supply, 2)
	assert.Len(t, row.Blend.Borrow, 1)

	// Single pool: the header mirrors the pool figures.
	require.NotNil(t, got.TotalValueUSD)
	assert.InDelta(t, 3619.267393206, *got.TotalValueUSD, 1e-9)
	require.NotNil(t, got.NetAPY)
	assert.InDelta(t, 2.5245032003462415, *got.NetAPY, 1e-9)
}

func TestGetAccountPositionsEmptyAccount(t *testing.T) {
	svc := NewPositionsService(&utils.MockWalletBackendService{}, nil, 0, nil)

	got, err := svc.GetAccountPositions(context.Background(), "GDW6QB3BFPQ3I4LH752JD2HYADFM2T4RVRCEUNCCH7MICWZR67NL5552", types.TESTNET)
	require.NoError(t, err)
	assert.NotNil(t, got.Positions)
	assert.Empty(t, got.Positions)
	require.NotNil(t, got.TotalValueUSD)
	assert.Equal(t, 0.0, *got.TotalValueUSD)
	assert.Nil(t, got.NetAPY)
}

func TestGetAccountPositionsUpstreamError(t *testing.T) {
	upErr := errors.New("wallet-backend on fire")
	svc := NewPositionsService(&utils.MockWalletBackendService{GetBlendPositionsError: upErr}, nil, 0, nil)

	_, err := svc.GetAccountPositions(context.Background(), "GDW6QB3BFPQ3I4LH752JD2HYADFM2T4RVRCEUNCCH7MICWZR67NL5552", types.TESTNET)
	assert.ErrorIs(t, err, upErr)
}
