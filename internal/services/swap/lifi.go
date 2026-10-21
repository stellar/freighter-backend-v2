package swap

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const lifiName = "lifi"

var errLifiMissingTx = errors.New("missing transaction")

type LifiConfig struct {
	Client         *LifiClient
	SwapContract   string
	FeeRecipient   string
	Aggregator     string
	MaxResourceFee int64
	Now            func() time.Time
}

type LifiSource struct {
	cfg LifiConfig
	m   *metrics.Swap
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
	fromDecimals, err := s.decimals(ctx, from)
	if err != nil {
		return nil, err
	}
	toDecimals, err := s.decimals(ctx, to)
	if err != nil {
		return nil, err
	}
	raw, err := ParseUnits(req.Amount, fromDecimals)
	if err != nil {
		return nil, fmt.Errorf("%w: amount %q", ErrBadRequest, req.Amount)
	}

	q, err := s.cfg.Client.Quote(ctx, fromContract, toContract, raw.String(), req.Account)
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
		return nil, fmt.Errorf("lifi: %w", errLifiMissingTx)
	}
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(q.TransactionRequest.Data, &env); err != nil {
		return nil, fmt.Errorf("lifi: %w", errLifiMissingTx)
	}
	call, err := decodeLifiCall(env)
	if err != nil {
		return nil, fmt.Errorf("lifi: %w", errLifiMissingTx)
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

func (s *LifiSource) decimals(ctx context.Context, a Asset) (int, error) {
	if a.IsClassic() {
		return ClassicDecimals, nil
	}
	return s.cfg.Client.TokenDecimals(ctx, a.Contract)
}

func lifiFromAmount(q *LifiQuote) (*big.Int, error) {
	v, ok := new(big.Int).SetString(q.Action.FromAmount, 10)
	if !ok {
		return nil, fmt.Errorf("lifi: bad fromAmount %q", q.Action.FromAmount)
	}
	return v, nil
}

// lifiValidFor bounds how far in the future LI.FI's transaction may expire.
const lifiValidFor = 600

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
	fromDecimals, err := s.decimals(ctx, from)
	if err != nil {
		return "", err
	}
	toDecimals, err := s.decimals(ctx, to)
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

	q, err := s.cfg.Client.Quote(ctx, fromContract, toContract, raw.String(), req.Account)
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
		return "", fmt.Errorf("lifi: %w", errLifiMissingTx)
	}
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(q.TransactionRequest.Data, &env); err != nil {
		return "", fmt.Errorf("lifi: %w", errLifiMissingTx)
	}
	if err := checkLifiTx(env, lifiExpect{
		Account:        req.Account,
		FromContract:   fromContract,
		ToContract:     toContract,
		SwapContract:   s.cfg.SwapContract,
		Aggregator:     s.cfg.Aggregator,
		FeeRecipient:   s.cfg.FeeRecipient,
		ToClassic:      to.IsClassic() && !to.IsNative(),
		Amount:         raw,
		MinOut:         minOut,
		MaxResourceFee: s.cfg.MaxResourceFee,
		MaxValidUntil:  s.cfg.Now().Unix() + lifiValidFor,
	}); err != nil {
		return "", err
	}

	// checkLifiTx guarantees V1 and SorobanData with 0 <= ResourceFee <= MaxResourceFee.
	total := new(big.Int).Add(big.NewInt(int64(env.V1.Tx.Ext.SorobanData.ResourceFee)), inclusion)
	if total.Cmp(big.NewInt(math.MaxUint32)) > 0 {
		return "", fmt.Errorf("%w: fee %q", ErrBadRequest, req.Fee)
	}
	env.V1.Tx.Fee = xdr.Uint32(total.Uint64())
	return xdr.MarshalBase64(env)
}
