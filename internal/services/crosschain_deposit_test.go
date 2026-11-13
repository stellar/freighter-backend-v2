// ABOUTME: Tests for the cross-chain deposit service: the cached chains list and the checked address flow.
// ABOUTME: Covers the configured check, active chain, trustline, row shape, and CREATE2 verification on both address calls.
package services

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

var testnetOnlyCfg = config.CrossMeshConfig{TestnetURL: "http://crossmesh.test", TestnetAPIKey: "test-key"}

func activeChains() *types.CrossMeshChains {
	return &types.CrossMeshChains{Chains: []types.CrossMeshChain{
		{Chain: "ethereum-sepolia", Status: "suspended"},
		{Chain: "polygon-amoy", Status: "active"},
	}}
}

// trustlinedRPC answers the trustline check with an existing account holding
// a USDC trustline with a non-zero limit.
func trustlinedRPC(t *testing.T) *utils.MockRPCService {
	t.Helper()
	acct, tl := accountEntry(t), trustlineEntry(t, 1)
	return &utils.MockRPCService{GetLedgerEntriesXDRFunc: func(keys []string, _ string) (map[string]string, error) {
		return map[string]string{keys[0]: acct, keys[1]: tl}, nil
	}}
}

func standardRow(address string) *types.CrossMeshDepositAddress {
	return &types.CrossMeshDepositAddress{
		DepositAddress: address, Recipient: vectorRecipient, DeliveryAsset: "USDC",
		Speed: "standard", Index: 0, CreatedAt: "2026-11-01T00:00:00Z",
	}
}

func TestCrossChainDepositService_GetChains(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("cached per network for 60s", func(t *testing.T) {
		t.Parallel()
		calls := 0
		cm := &utils.MockCrossMeshService{GetChainsFunc: func(context.Context, string) (*types.CrossMeshChains, error) {
			calls++
			return activeChains(), nil
		}}
		cache := newFakeJSONCache()
		svc := NewCrossChainDepositService(cm, nil, cache, testnetOnlyCfg, nil)

		first, err := svc.GetChains(ctx, types.TESTNET)
		require.NoError(t, err)
		second, err := svc.GetChains(ctx, types.TESTNET)
		require.NoError(t, err)

		assert.Equal(t, 1, calls, "the second call must be served from the cache")
		assert.Equal(t, first, second)
		assert.Equal(t, CrossMeshChainsCacheTTL, onlyTTL(t, cache))
	})

	t.Run("nil cache fetches every time", func(t *testing.T) {
		t.Parallel()
		calls := 0
		cm := &utils.MockCrossMeshService{GetChainsFunc: func(context.Context, string) (*types.CrossMeshChains, error) {
			calls++
			return activeChains(), nil
		}}
		svc := NewCrossChainDepositService(cm, nil, nil, testnetOnlyCfg, nil)
		for range 2 {
			_, err := svc.GetChains(ctx, types.TESTNET)
			require.NoError(t, err)
		}
		assert.Equal(t, 2, calls)
	})

	t.Run("upstream error is chains unavailable", func(t *testing.T) {
		t.Parallel()
		upstream := &CrossMeshError{Status: 500}
		cm := &utils.MockCrossMeshService{GetChainsFunc: func(context.Context, string) (*types.CrossMeshChains, error) {
			return nil, upstream
		}}
		svc := NewCrossChainDepositService(cm, nil, newFakeJSONCache(), testnetOnlyCfg, nil)
		_, err := svc.GetChains(ctx, types.TESTNET)
		require.ErrorIs(t, err, ErrChainsUnavailable)
		require.ErrorIs(t, err, upstream)
	})

	t.Run("unconfigured networks", func(t *testing.T) {
		t.Parallel()
		svc := NewCrossChainDepositService(&utils.MockCrossMeshService{}, nil, nil, testnetOnlyCfg, nil)
		for _, network := range []string{types.PUBLIC, types.FUTURENET} {
			_, err := svc.GetChains(ctx, network)
			require.ErrorIs(t, err, ErrCrossChainNotConfigured, network)
		}
	})
}

// Not parallel: it swaps the process-global logger to check the
// verification-failure log line.
func TestCrossChainDepositService_Address(t *testing.T) {
	ctx := context.Background()
	upstreamErr := &CrossMeshError{Status: 503, Code: "unavailable"}
	rpcErr := errors.New("rpc down")
	lowercase := strings.ToLower(vectorStandard)

	cases := map[string]struct {
		network string
		chains  *types.CrossMeshChains
		rpc     func(t *testing.T) *utils.MockRPCService
		row     *types.CrossMeshDepositAddress
		rowErr  error
		want    error
	}{
		"futurenet is not configured": {network: types.FUTURENET, want: ErrCrossChainNotConfigured},
		"pubnet without key":          {network: types.PUBLIC, want: ErrCrossChainNotConfigured},
		"no active chain": {
			chains: &types.CrossMeshChains{Chains: []types.CrossMeshChain{{Status: "suspended"}, {Status: "paused"}}},
			want:   ErrNoActiveChain,
		},
		"account not found": {
			rpc: func(*testing.T) *utils.MockRPCService {
				return &utils.MockRPCService{GetLedgerEntriesXDRFunc: func([]string, string) (map[string]string, error) {
					return map[string]string{}, nil
				}}
			},
			want: ErrAccountNotFound,
		},
		"trustline missing": {
			rpc: func(t *testing.T) *utils.MockRPCService {
				acct := accountEntry(t)
				return &utils.MockRPCService{GetLedgerEntriesXDRFunc: func(keys []string, _ string) (map[string]string, error) {
					return map[string]string{keys[0]: acct}, nil
				}}
			},
			want: ErrTrustlineMissing,
		},
		"rpc error": {
			rpc: func(*testing.T) *utils.MockRPCService {
				return &utils.MockRPCService{GetLedgerEntriesXDRFunc: func([]string, string) (map[string]string, error) {
					return nil, rpcErr
				}}
			},
			want: ErrTrustlineCheckFailed,
		},
		"upstream error passes through": {rowErr: upstreamErr, want: upstreamErr},
		"no address yet":                {row: nil, want: ErrNoDepositAddress},
		"fast row": {
			row:  &types.CrossMeshDepositAddress{DepositAddress: vectorStandard, DeliveryAsset: "USDC", Speed: "fast"},
			want: ErrUnexpectedDepositAddress,
		},
		"non-USDC row": {
			row:  &types.CrossMeshDepositAddress{DepositAddress: vectorStandard, DeliveryAsset: "XLM", Speed: "standard"},
			want: ErrUnexpectedDepositAddress,
		},
		"address mismatch": {row: standardRow(vectorFast), want: ErrVerificationFailed},
		"wrong index":      {row: &types.CrossMeshDepositAddress{DepositAddress: vectorStandard, DeliveryAsset: "USDC", Speed: "standard", Index: 1}, want: ErrVerificationFailed},
		"lowercase upstream returns derived EIP-55": {row: standardRow(lowercase)},
	}

	for name, tc := range cases {
		for _, method := range []string{"GetOrCreateAddress", "LookupAddress"} {
			if tc.want == ErrNoDepositAddress && method == "GetOrCreateAddress" {
				continue // create always answers with a row
			}
			t.Run(name+"/"+method, func(t *testing.T) {
				var logs bytes.Buffer
				logger.SetOutput(&logs)
				t.Cleanup(func() { logger.SetOutput(os.Stdout) })

				network := tc.network
				if network == "" {
					network = types.TESTNET
				}
				chains := tc.chains
				if chains == nil {
					chains = activeChains()
				}
				rpc := trustlinedRPC(t)
				if tc.rpc != nil {
					rpc = tc.rpc(t)
				}
				fetch := func(_ context.Context, gotNetwork, recipient string) (*types.CrossMeshDepositAddress, error) {
					assert.Equal(t, types.TESTNET, gotNetwork)
					assert.Equal(t, vectorRecipient, recipient)
					return tc.row, tc.rowErr
				}
				cm := &utils.MockCrossMeshService{
					GetChainsFunc:            func(context.Context, string) (*types.CrossMeshChains, error) { return chains, nil },
					CreateDepositAddressFunc: fetch,
					LookupDepositAddressFunc: fetch,
				}
				m := metrics.NewCrossChain(prometheus.NewRegistry())
				svc := NewCrossChainDepositService(cm, rpc, nil, testnetOnlyCfg, m)

				call := svc.GetOrCreateAddress
				if method == "LookupAddress" {
					call = svc.LookupAddress
				}
				got, err := call(ctx, network, vectorRecipient)

				failures := testutil.ToFloat64(m.VerificationFailures.WithLabelValues(types.TESTNET))
				if tc.want == ErrVerificationFailed {
					assert.Equal(t, float64(1), failures)
					assert.Contains(t, logs.String(), "deposit address failed verification")
					assert.NotContains(t, logs.String(), testnetOnlyCfg.TestnetAPIKey)
				} else {
					assert.Zero(t, failures)
					assert.Empty(t, logs.String())
				}
				if tc.want != nil {
					require.ErrorIs(t, err, tc.want)
					assert.Nil(t, got)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, &DepositAddress{
					DepositAddress: vectorStandard,
					Account:        vectorRecipient,
					Index:          0,
					CreatedAt:      "2026-11-01T00:00:00Z",
				}, got)
			})
		}
	}
}
