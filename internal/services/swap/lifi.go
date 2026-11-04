package swap

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"golang.org/x/sync/errgroup"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const lifiName = "lifi"

// lifiQuoteSlippage is sent with every quote. A quote shows estimate.toAmount,
// which slippage doesn't change; builds send the user's own slippage.
const lifiQuoteSlippage = "0.005"

// SlippageFraction converts a slippage percent in [0, 10], written as digits
// with an optional ".digits" part, to the decimal fraction LI.FI expects
// ("0.5" -> "0.005"). big.Rat alone would also accept "1e1" and "1/2".
func SlippageFraction(percent string) (string, error) {
	intPart, frac, hasDot := strings.Cut(percent, ".")
	if len(percent) > 18 || intPart == "" || (hasDot && frac == "") ||
		strings.Trim(intPart+frac, "0123456789") != "" {
		return "", errors.New("invalid slippage")
	}
	r, ok := new(big.Rat).SetString(percent)
	if !ok || r.Cmp(big.NewRat(10, 1)) > 0 {
		return "", errors.New("invalid slippage")
	}
	r.Quo(r, big.NewRat(100, 1))
	out := r.FloatString(len(frac) + 2)
	if strings.Contains(out, ".") {
		out = strings.TrimRight(strings.TrimRight(out, "0"), ".")
	}
	return out, nil
}

// Mainnet LI.FI values. The transaction checks pin to these, so a change
// goes through code review rather than an environment variable.
const (
	LifiBaseURL        = "https://li.quest"
	LifiSwapContract   = "CDCNXZHYWRDHNAQEY5EX3WCF7BCWVJBS64ZBVUGRTQBFTHUKG6NAXFTR"
	LifiFeeRecipient   = "GAZYY5SJZNAVWZWVKJ2E74W2WKZP5BACLVV6FVHC5NZWXPKTQEDOYUR4"
	LifiPartnerID      = "104aaf592"
	SoroswapAggregator = "CARVQXFP4JF5ELLXUMQ6DALR346YVGBMQOHB4ENA7SSVXAYABXLBDDC4"
)

// MaxTimeoutSeconds bounds a built swap's validity.
const MaxTimeoutSeconds = 86400

// timeoutSeconds rechecks the handler's bound: a 0 timeout would build a
// transaction that never expires.
func timeoutSeconds(req types.SwapBuildRequest) (int64, error) {
	if req.Timeout < 1 || req.Timeout > MaxTimeoutSeconds {
		return 0, fmt.Errorf("%w: timeout %d", ErrBadRequest, req.Timeout)
	}
	return req.Timeout, nil
}

// ErrLifiMissingTx is a 200 from LI.FI without a usable transaction.
var ErrLifiMissingTx = errors.New("lifi returned no usable transaction")

type LifiConfig struct {
	Client       *LifiClient
	SwapContract string
	FeeRecipient string
	PartnerID    string
	Aggregator   string
	RPC          types.RPCService
	Now          func() time.Time
}

type LifiSource struct {
	cfg      LifiConfig
	m        *metrics.Swap
	decimals sync.Map // contract → int
	sac      sync.Map // contract → bool: a non-native Stellar Asset Contract
}

func NewLifiSource(cfg LifiConfig, m *metrics.Swap) *LifiSource {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &LifiSource{cfg: cfg, m: m}
}

func (s *LifiSource) Name() string { return lifiName }

func (s *LifiSource) Supports(network string) bool { return network == types.PUBLIC }

var lifiFeeNames = map[string]string{"LIFI Fixed Fee": "LI.FI fee", "Soroswap Fee": "Soroswap fee"}

func (s *LifiSource) Quote(ctx context.Context, req types.SwapQuoteRequest) (*types.SwapRoute, error) {
	if req.Mode == types.SwapModeReceive {
		return nil, ErrUnsupportedMode
	}
	from, err := ParseAsset(req.FromAsset)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	to, err := ParseAsset(req.ToAsset)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	fromContract, err := from.ContractID(req.Network)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	toContract, err := to.ContractID(req.Network)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	fromDecimals, toDecimals, err := s.pairDecimals(ctx, req.Account, from, to)
	if err != nil {
		return nil, err
	}
	raw, err := ParseUnits(req.Amount, fromDecimals)
	if err != nil {
		return nil, fmt.Errorf("%w: amount %q", ErrBadRequest, req.Amount)
	}

	q, err := s.cfg.Client.Quote(ctx, fromContract, toContract, raw.String(), req.Account, lifiQuoteSlippage)
	if err != nil {
		return nil, err
	}
	// LI.FI silently lowers fromAmount to the spendable balance.
	quoted, err := lifiFromAmount(q)
	if err != nil {
		return nil, err
	}
	if quoted.Cmp(raw) != 0 {
		return nil, ErrInsufficientBalance
	}
	if q.TransactionRequest == nil {
		return nil, fmt.Errorf("lifi: %w", ErrLifiMissingTx)
	}
	var env xdr.TransactionEnvelope
	if err = xdr.SafeUnmarshalBase64(q.TransactionRequest.Data, &env); err != nil {
		return nil, fmt.Errorf("lifi: %w", ErrLifiMissingTx)
	}
	call, err := decodeLifiCall(env)
	if err != nil {
		return nil, fmt.Errorf("lifi: %w", ErrLifiMissingTx)
	}

	toAmount, ok := new(big.Int).SetString(q.Estimate.ToAmount, 10)
	if !ok {
		return nil, fmt.Errorf("lifi: bad toAmount %q", q.Estimate.ToAmount)
	}
	fees := make([]types.SwapFee, 0, len(q.Estimate.FeeCosts))
	for _, fc := range q.Estimate.FeeCosts {
		amt, ok := new(big.Int).SetString(fc.Amount, 10)
		if !ok {
			return nil, fmt.Errorf("lifi: bad fee amount %q", fc.Amount)
		}
		if amt.Sign() < 0 || fc.Token.Address != fromContract {
			return nil, fmt.Errorf("lifi: fee %q is negative or not in the from token", fc.Name)
		}
		name := fc.Name
		if n, ok := lifiFeeNames[name]; ok {
			name = n
		}
		fees = append(fees, types.SwapFee{Name: name, Amount: FormatUnits(amt, fromDecimals), Asset: req.FromAsset})
	}
	return &types.SwapRoute{
		Source:     lifiName,
		FromAmount: req.Amount,
		ToAmount:   FormatUnits(toAmount, toDecimals),
		Paths:      lifiPaths(call, from, to, fromContract, toContract, s.m),
		Fees:       fees,
	}, nil
}

// maxTokenDecimals caps a token's decimals() before ParseUnits and FormatUnits
// size strings by it; any contract can return up to MaxUint32. Token amounts are
// i128 (max ~1.7e38), so past 38 decimals no balance can hold one whole token.
const maxTokenDecimals = 38

// pairDecimals reads both tokens together so a cold pair costs one round trip.
func (s *LifiSource) pairDecimals(ctx context.Context, account string, from, to Asset) (int, int, error) {
	var fromDec, toDec int
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { fromDec, err = s.tokenDecimals(gctx, account, from); return })
	g.Go(func() (err error) { toDec, err = s.tokenDecimals(gctx, account, to); return })
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return fromDec, toDec, nil
}

// tokenDecimals reads a Soroban token's decimals() on-chain: LI.FI's own value
// would make checkLifiTx compare its transaction against its own numbers.
func (s *LifiSource) tokenDecimals(ctx context.Context, account string, a Asset) (int, error) {
	if a.IsClassic() {
		return ClassicDecimals, nil
	}
	if v, ok := s.decimals.Load(a.Contract); ok {
		return v.(int), nil
	}
	id, err := utils.ScAddressFromString(a.Contract)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	res, err := s.cfg.RPC.SimulateInvocation(ctx, *id, &txnbuild.SimpleAccount{AccountID: account}, "decimals", nil, txnbuild.NewTimeout(300), types.PUBLIC)
	if err != nil {
		return 0, &metrics.UpstreamError{Kind: "simulation_error", Err: fmt.Errorf("token %s decimals: %w", a.Contract, err)}
	}
	// Checked before caching: ParseUnits/FormatUnits size strings by decimals.
	if res == nil || res.Type != xdr.ScValTypeScvU32 || res.U32 == nil || *res.U32 > maxTokenDecimals {
		return 0, &metrics.UpstreamError{Kind: "invalid_response", Err: fmt.Errorf("token %s: unusable decimals() result", a.Contract)}
	}
	d := int(*res.U32)
	s.decimals.Store(a.Contract, d)
	return d, nil
}

// toClassic reports whether the destination is a classic asset LI.FI may open a
// trustline for: written as CODE:ISSUER, or given as the SAC id of one. Only the
// contract instance can tell a SAC from a token whose own trust() is arbitrary code.
func (s *LifiSource) toClassic(ctx context.Context, net string, to Asset) (bool, error) {
	if to.IsClassic() {
		return !to.IsNative(), nil
	}
	native, err := Asset{ID: "XLM"}.ContractID(net)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if to.Contract == native {
		return false, nil
	}
	if v, ok := s.sac.Load(to.Contract); ok {
		return v.(bool), nil
	}
	addr, err := utils.ScAddressFromString(to.Contract)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	key := xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
		Contract:   *addr,
		Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Durability: xdr.ContractDataDurabilityPersistent,
	}}
	keyB64, err := xdr.MarshalBase64(key)
	if err != nil {
		return false, err
	}
	entries, err := s.cfg.RPC.GetLedgerEntriesXDR(ctx, []string{keyB64}, net)
	if err != nil {
		return false, &metrics.UpstreamError{Kind: "rpc_error", Err: fmt.Errorf("contract %s instance: %w", to.Contract, err)}
	}
	b64, ok := entries[keyB64]
	if !ok {
		// Not cached: the entry may be archived or briefly missing, not absent for good.
		return false, nil
	}
	var data xdr.LedgerEntryData
	if err := xdr.SafeUnmarshalBase64(b64, &data); err != nil {
		return false, &metrics.UpstreamError{Kind: "invalid_response", Err: fmt.Errorf("contract %s instance: %w", to.Contract, err)}
	}
	isSAC := false
	if cd, ok := data.GetContractData(); ok {
		if inst, ok := cd.Val.GetInstance(); ok {
			isSAC = inst.Executable.Type == xdr.ContractExecutableTypeContractExecutableStellarAsset
		}
	}
	s.sac.Store(to.Contract, isSAC)
	return isSAC, nil
}

func lifiFromAmount(q *LifiQuote) (*big.Int, error) {
	v, ok := new(big.Int).SetString(q.Action.FromAmount, 10)
	if !ok {
		return nil, &metrics.UpstreamError{Kind: "invalid_response", Err: fmt.Errorf("lifi: bad fromAmount %q", q.Action.FromAmount)}
	}
	return v, nil
}

func (s *LifiSource) BuildTx(ctx context.Context, req types.SwapBuildRequest) (string, error) {
	from, err := ParseAsset(req.FromAsset)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	to, err := ParseAsset(req.ToAsset)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	fromContract, err := from.ContractID(req.Network)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	toContract, err := to.ContractID(req.Network)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	fromDecimals, toDecimals, err := s.pairDecimals(ctx, req.Account, from, to)
	if err != nil {
		return "", err
	}
	toClassic, err := s.toClassic(ctx, req.Network, to)
	if err != nil {
		return "", err
	}
	raw, err := ParseUnits(req.Amount, fromDecimals)
	if err != nil {
		return "", fmt.Errorf("%w: amount %q", ErrBadRequest, req.Amount)
	}
	minOut, err := ParseUnits(req.MinOut, toDecimals)
	if err != nil {
		return "", fmt.Errorf("%w: minOut %q", ErrBadRequest, req.MinOut)
	}
	inclusion, err := ParseUnits(req.Fee, ClassicDecimals)
	if err != nil {
		return "", fmt.Errorf("%w: fee %q", ErrBadRequest, req.Fee)
	}
	if inclusion.Cmp(big.NewInt(txnbuild.MinBaseFee)) < 0 {
		inclusion.SetInt64(txnbuild.MinBaseFee)
	}

	slippage, err := SlippageFraction(req.Slippage)
	if err != nil {
		return "", fmt.Errorf("%w: slippage %q", ErrBadRequest, req.Slippage)
	}
	q, err := s.cfg.Client.Quote(ctx, fromContract, toContract, raw.String(), req.Account, slippage)
	if errors.Is(err, ErrNoRoute) || errors.Is(err, ErrInsufficientBalance) {
		return "", fmt.Errorf("lifi: %v: %w", err, ErrQuoteMoved)
	}
	if err != nil {
		return "", err
	}
	quoted, err := lifiFromAmount(q)
	if err != nil {
		return "", err
	}
	if quoted.Cmp(raw) != 0 {
		return "", fmt.Errorf("lifi: amount shrank: %w", ErrQuoteMoved)
	}
	if q.TransactionRequest == nil {
		return "", fmt.Errorf("lifi: %w", ErrLifiMissingTx)
	}
	var env xdr.TransactionEnvelope
	if err = xdr.SafeUnmarshalBase64(q.TransactionRequest.Data, &env); err != nil {
		return "", fmt.Errorf("lifi: %w", ErrLifiMissingTx)
	}
	if err = checkLifiTx(env, lifiExpect{
		Account:      req.Account,
		FromContract: fromContract,
		ToContract:   toContract,
		SwapContract: s.cfg.SwapContract,
		Aggregator:   s.cfg.Aggregator,
		FeeRecipient: s.cfg.FeeRecipient,
		PartnerID:    s.cfg.PartnerID,
		ToClassic:    toClassic,
		Amount:       raw,
		MinOut:       minOut,
	}); err != nil {
		return "", err
	}

	// checkLifiTx guarantees V1 and SorobanData with ResourceFee >= 0.
	total := new(big.Int).Add(big.NewInt(int64(env.V1.Tx.Ext.SorobanData.ResourceFee)), inclusion)
	if total.Cmp(big.NewInt(math.MaxUint32)) > 0 {
		return "", fmt.Errorf("%w: fee %q", ErrBadRequest, req.Fee)
	}
	env.V1.Tx.Fee = xdr.Uint32(total.Uint64())
	// Safe to move: the tx is unsigned and its source-account auth has no expiry of its own.
	timeout, err := timeoutSeconds(req)
	if err != nil {
		return "", err
	}
	tb := env.V1.Tx.Cond.TimeBounds
	tb.MaxTime = xdr.TimePoint(s.cfg.Now().Unix() + timeout)
	if tb.MinTime > tb.MaxTime {
		return "", checkFailed("time bounds")
	}
	env.V1.Signatures = nil
	return xdr.MarshalBase64(env)
}
