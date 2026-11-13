// ABOUTME: Cross-chain deposit flow over Cross Mesh: the per-network chains list (cached) and the deposit address calls.
// ABOUTME: Every address answer passes the active-chain, USDC trustline, row shape, and CREATE2 checks before it is returned.
package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

var (
	// ErrCrossChainNotConfigured means the network has no configured Cross Mesh
	// environment (no API key, or no pinned contracts).
	ErrCrossChainNotConfigured = errors.New("cross-chain deposits not configured for network")
	// ErrChainsUnavailable wraps any failure to fetch the chains list.
	ErrChainsUnavailable = errors.New("cross mesh chains unavailable")
	// ErrNoActiveChain means no source chain has status "active" (TR-14).
	ErrNoActiveChain = errors.New("no active source chain")
	// ErrTrustlineCheckFailed wraps an RPC failure during the trustline check.
	ErrTrustlineCheckFailed = errors.New("usdc trustline check failed")
	// ErrNoDepositAddress means the account has no deposit address yet (TR-10).
	ErrNoDepositAddress = errors.New("no deposit address")
	// ErrUnexpectedDepositAddress means Cross Mesh answered with a row that is
	// not a standard-speed USDC address.
	ErrUnexpectedDepositAddress = errors.New("unexpected deposit address row")
	// ErrVerificationFailed means the upstream address did not match the
	// local CREATE2 derivation (TR-24).
	ErrVerificationFailed = errors.New("deposit address verification failed")
)

// DepositAddress is a verified deposit address for one Stellar account.
// DepositAddress is the locally derived EIP-55 form.
type DepositAddress struct {
	DepositAddress string `json:"deposit_address"`
	Account        string `json:"account"`
	Index          uint64 `json:"index"`
	CreatedAt      string `json:"created_at"`
}

// CrossChainDepositService serves the cross-chain deposit routes.
type CrossChainDepositService struct {
	crossMesh types.CrossMeshService
	rpc       types.RPCService
	cache     JSONCache
	cfg       config.CrossMeshConfig
	metrics   *metrics.CrossChain
}

// NewCrossChainDepositService builds the service. cache and m may be nil:
// without a cache every chains call goes upstream.
func NewCrossChainDepositService(crossMesh types.CrossMeshService, rpc types.RPCService, cache JSONCache, cfg config.CrossMeshConfig, m *metrics.CrossChain) *CrossChainDepositService {
	return &CrossChainDepositService{crossMesh: crossMesh, rpc: rpc, cache: cache, cfg: cfg, metrics: m}
}

func chainsCacheKey(network string) string {
	return "crossmesh:chains:v1:" + network
}

// GetChains returns the network's chains list, cached for
// CrossMeshChainsCacheTTL. A cache failure falls through to Cross Mesh; an
// upstream failure is returned as ErrChainsUnavailable with no stale fallback.
func (s *CrossChainDepositService) GetChains(ctx context.Context, network string) (*types.CrossMeshChains, error) {
	if !CrossMeshConfigured(s.cfg, network) {
		return nil, ErrCrossChainNotConfigured
	}
	key := chainsCacheKey(network)
	if s.cache != nil {
		hits, err := s.cache.MGetJSON(ctx, []string{key}, func() any { return new(types.CrossMeshChains) })
		if err != nil {
			logger.Warn("crosschain: redis MGet failed; bypassing cache", "network", network, "error", err)
		} else if hit, ok := hits[key].(*types.CrossMeshChains); ok {
			return hit, nil
		}
	}

	chains, err := s.crossMesh.GetChains(ctx, network)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrChainsUnavailable, err)
	}
	if s.cache != nil {
		wctx, cancel := cacheWriteContext(ctx)
		defer cancel()
		if err = s.cache.SetJSON(wctx, key, chains, CrossMeshChainsCacheTTL); err != nil {
			logger.Warn("crosschain: redis SET failed", "network", network, "error", err)
		}
	}
	return chains, nil
}

// GetOrCreateAddress returns the account's deposit address, creating it on
// Cross Mesh if needed, after every check in address.
func (s *CrossChainDepositService) GetOrCreateAddress(ctx context.Context, network, account string) (*DepositAddress, error) {
	return s.address(ctx, network, account, s.crossMesh.CreateDepositAddress)
}

// LookupAddress returns the account's existing deposit address after every
// check in address, or ErrNoDepositAddress when it has none.
func (s *CrossChainDepositService) LookupAddress(ctx context.Context, network, account string) (*DepositAddress, error) {
	return s.address(ctx, network, account, s.crossMesh.LookupDepositAddress)
}

// address runs the checks every address answer must pass, in order: the
// network is configured, a chain is active, the account trusts USDC, fetch
// returns a standard USDC row, and its address matches the CREATE2
// derivation for the requested account.
func (s *CrossChainDepositService) address(ctx context.Context, network, account string, fetch func(ctx context.Context, network, recipient string) (*types.CrossMeshDepositAddress, error)) (*DepositAddress, error) {
	chains, err := s.GetChains(ctx, network)
	if err != nil {
		return nil, err
	}
	if !hasActiveChain(chains) {
		return nil, ErrNoActiveChain
	}

	env, _ := CrossMeshEnvFor(network)
	if err = CheckUSDCTrustline(ctx, s.rpc, network, account, env.USDCIssuer); err != nil {
		if errors.Is(err, ErrAccountNotFound) || errors.Is(err, ErrTrustlineMissing) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrTrustlineCheckFailed, err)
	}

	row, err := fetch(ctx, network, account)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNoDepositAddress
	}
	if row.Speed != crossMeshSpeed || row.DeliveryAsset != crossMeshDeliveryAsset {
		return nil, fmt.Errorf("%w: speed %q, delivery asset %q", ErrUnexpectedDepositAddress, row.Speed, row.DeliveryAsset)
	}

	derived, ok := VerifyDepositAddress(row.DepositAddress, account, row.Index, env.Factory, env.Implementation)
	if !ok {
		metrics.RecordVerificationFailure(s.metrics, network)
		logger.ErrorWithContext(ctx, "crosschain: deposit address failed verification",
			"network", network, "account", account, "index", row.Index, "upstream", row.DepositAddress, "derived", derived)
		return nil, ErrVerificationFailed
	}
	return &DepositAddress{DepositAddress: derived, Account: account, Index: row.Index, CreatedAt: row.CreatedAt}, nil
}

func hasActiveChain(chains *types.CrossMeshChains) bool {
	for _, c := range chains.Chains {
		if c.Status == "active" {
			return true
		}
	}
	return false
}
