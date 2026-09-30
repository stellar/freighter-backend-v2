package swap

import (
	"context"
	"errors"
	"fmt"
	xoxno "github.com/xoxno/sdk-go"
	"math/big"
	"sync"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const quoteServiceName = "swap-quote"

var (
	// errUnsupported means the source cannot quote this pair or network.
	errUnsupported = errors.New("swap pair not supported by source")
	// errInvalidQuote means the source answered with a quote that failed validation.
	errInvalidQuote = errors.New("swap quote failed validation")
)

// Alternative error codes returned to clients. They are short and stable on
// purpose: upstream error text never reaches the client.
const (
	altNoRoute     = "no_route"
	altUnsupported = "unsupported"
	altInvalid     = "invalid_quote"
	altTimeout     = "timeout"
	altUnavailable = "unavailable"
)

// candidate is one source's answer to a SwapQuoteRequest. Amounts are
// atomic integers so candidates from different sources compare exactly.
type candidate struct {
	Source        string
	DestAmount    *big.Int
	DestAmountMin *big.Int
	DestDecimals  int
	// Path lists the intermediate classic assets ("native" or "CODE:ISSUER"). Horizon only.
	Path []string
	// Route lists the pools the swap crosses. Soroban aggregator only.
	Route       []types.SwapRouteHop
	Transaction *types.SwapTransaction
	PriceImpact *float64
	// RequiresTrustline marks a route that cannot carry a transaction until the
	// sender opens the destination trustline. Soroban aggregator only.
	RequiresTrustline bool
	// NetworkFee is the total fee in stroops of the transaction in Transaction.
	// Nil when the client builds the transaction itself.
	NetworkFee *big.Int
}

// source produces a candidate route from one liquidity venue set.
type source interface {
	types.Service
	Quote(ctx context.Context, req types.SwapQuoteRequest) (*candidate, error)
	// QuoteInput returns the source amount, in atoms, this source needs to
	// deliver req.DestAmount.
	QuoteInput(ctx context.Context, req types.SwapQuoteRequest) (*big.Int, error)
}

type quoteService struct {
	// sources are ordered by tie-break preference: on an equal output the
	// earlier source wins, so the cheaper classic transaction beats a Soroban one.
	sources       []source
	sourceTimeout time.Duration
	svcMetrics    *metrics.Service
}

// newQuoteService fans a request out to every source in parallel and
// returns the candidate with the highest destination amount. sources must be
// ordered by tie-break preference.
func newQuoteService(sources []source, sourceTimeout time.Duration, svcMetrics *metrics.Service) types.SwapQuoteService {
	return &quoteService{sources: sources, sourceTimeout: sourceTimeout, svcMetrics: svcMetrics}
}

func (s *quoteService) Name() string { return quoteServiceName }

// askSources runs ask on every source in parallel, each with its own timeout.
// Answers and errors are index-aligned with the sources.
func askSources[T any](ctx context.Context, sources []source, timeout time.Duration, ask func(context.Context, source) (T, error)) ([]T, []error) {
	answers := make([]T, len(sources))
	errs := make([]error, len(sources))

	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Go(func() {
			srcCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			answers[i], errs[i] = ask(srcCtx, src)
		})
	}
	wg.Wait()
	return answers, errs
}

func (s *quoteService) GetBestQuote(ctx context.Context, req types.SwapQuoteRequest) (_ *types.SwapQuote, err error) {
	defer recordQuoteCall(s.svcMetrics, "GetBestQuote", req.Network, time.Now(), &err)

	timeout := s.sourceTimeout
	if req.DestAmount != "" {
		// Receive quotes need two rounds; keep their total within one source budget.
		timeout /= 2
		input, err := s.sizeInput(ctx, req, timeout)
		if err != nil {
			return nil, err
		}
		req.SourceAmount = formatAtomic(input, req.SourceDecimals)
	}

	cands, errs := askSources(ctx, s.sources, timeout, func(c context.Context, src source) (*candidate, error) {
		return src.Quote(c, req)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	best := -1
	alts := make([]types.SwapQuoteAlternative, len(s.sources))
	for i, src := range s.sources {
		alts[i].Source = src.Name()
		if errs[i] != nil {
			alts[i].Error = altCode(errs[i])
			logSourceFailure(ctx, req, src.Name(), errs[i])
			continue
		}
		alts[i].DestinationAmount = formatAtomic(cands[i].DestAmount, cands[i].DestDecimals)
		if best == -1 || cands[i].DestAmount.Cmp(cands[best].DestAmount) > 0 {
			best = i
		}
	}
	if best == -1 {
		return nil, noAnswer(errs)
	}
	alts[best].Selected = true

	return buildQuote(req, cands[best], alts)
}

// sizeInput returns the smallest source amount any source needs to deliver
// req.DestAmount. Sizing is only a starting point: the executable quote is then
// asked for at that input, so every venue competes on the same amount and the
// transaction is built for a fixed input with the user's slippage on the output.
func (s *quoteService) sizeInput(ctx context.Context, req types.SwapQuoteRequest, timeout time.Duration) (*big.Int, error) {
	inputs, errs := askSources(ctx, s.sources, timeout, func(c context.Context, src source) (*big.Int, error) {
		return src.QuoteInput(c, req)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var best *big.Int
	for i, in := range inputs {
		if errs[i] != nil {
			logSourceFailure(ctx, req, s.sources[i].Name(), errs[i])
			continue
		}
		if best == nil || in.Cmp(best) < 0 {
			best = in
		}
	}
	if best == nil {
		return nil, noAnswer(errs)
	}
	return best, nil
}

// noAnswer is the error when no source answered: an upstream failure if any
// source failed that way, else "no route".
func noAnswer(errs []error) error {
	var upstream []error
	for _, err := range errs {
		if code := altCode(err); err != nil && (code == altTimeout || code == altUnavailable) {
			upstream = append(upstream, err)
		}
	}
	if len(upstream) > 0 {
		return fmt.Errorf("all swap quote sources failed: %w", errors.Join(upstream...))
	}
	return types.ErrSwapNoRoute
}

// logSourceFailure logs a source failure that is not a normal "no route" answer.
func logSourceFailure(ctx context.Context, req types.SwapQuoteRequest, name string, err error) {
	if code := altCode(err); code != altNoRoute && code != altUnsupported {
		logger.Global().WarnContext(ctx, "swap quote: source failed", "source", name, "network", req.Network, "code", code, "error", err)
	}
}

func altCode(err error) string {
	switch {
	case errors.Is(err, types.ErrSwapNoRoute):
		return altNoRoute
	case errors.Is(err, errUnsupported):
		return altUnsupported
	case errors.Is(err, errInvalidQuote):
		return altInvalid
	case errors.Is(err, context.DeadlineExceeded):
		return altTimeout
	default:
		return altUnavailable
	}
}

func buildQuote(req types.SwapQuoteRequest, c *candidate, alts []types.SwapQuoteAlternative) (*types.SwapQuote, error) {
	srcAtoms, err := xoxno.ParseDecimalAmount(req.SourceAmount, req.SourceDecimals)
	if err != nil {
		return nil, err
	}
	var feeXlm *string
	if c.NetworkFee != nil {
		f := formatAtomic(c.NetworkFee, types.ClassicDecimals)
		feeXlm = &f
	}
	return &types.SwapQuote{
		NetworkFeeXlm:        feeXlm,
		RequiresTrustline:    c.RequiresTrustline,
		Source:               c.Source,
		SourceAmount:         req.SourceAmount,
		DestinationAmount:    formatAtomic(c.DestAmount, c.DestDecimals),
		DestinationAmountMin: formatAtomic(c.DestAmountMin, c.DestDecimals),
		DestinationDecimals:  c.DestDecimals,
		ConversionRate:       conversionRate(srcAtoms, req.SourceDecimals, c.DestAmount, c.DestDecimals),
		Path:                 c.Path,
		Route:                c.Route,
		Transaction:          c.Transaction,
		PriceImpact:          c.PriceImpact,
		Alternatives:         alts,
	}, nil
}

// formatAtomic renders atomic units with a fixed number of decimals, matching
// Horizon's own decimal strings ("1.2345670").
func formatAtomic(v *big.Int, decimals int) string {
	return new(big.Rat).SetFrac(v, pow10(decimals)).FloatString(decimals)
}

func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

// minAmountOut returns floor(amount × (1 − slippage)) with the tolerance
// rounded down to parts per million, the same rule the aggregator applies, so
// a source-independent floor never permits more slippage than requested.
func minAmountOut(amount *big.Int, slippagePercent float64) *big.Int {
	slipPPM := max(0, min(int64(slippagePercent*10_000), 1_000_000))
	retained := big.NewInt(1_000_000 - slipPPM)
	out := new(big.Int).Mul(amount, retained)
	return out.Quo(out, big.NewInt(1_000_000))
}

func conversionRate(srcAtoms *big.Int, srcDecimals int, dstAtoms *big.Int, dstDecimals int) string {
	num := new(big.Int).Mul(dstAtoms, pow10(srcDecimals))
	den := new(big.Int).Mul(srcAtoms, pow10(dstDecimals))
	return new(big.Rat).SetFrac(num, den).FloatString(7)
}

// Config configures the swap quote service.
type Config struct {
	HorizonPubnetURL  string
	HorizonTestnetURL string
	// Networks holds the aggregator settings per network. Empty switches the
	// aggregator source off.
	Networks map[string]Network
	// SourceTimeout bounds each source's answer.
	SourceTimeout time.Duration
}

// NewQuoteService wires Horizon and the enabled XOXNO/LI.FI sources.
// Horizon comes first so an exact tie
// resolves to the cheaper classic transaction.
func NewQuoteService(cfg Config, svcMetrics *metrics.Service) types.SwapQuoteService {
	horizon := newHorizonClient(cfg.HorizonPubnetURL, cfg.HorizonTestnetURL)
	sources := []source{newHorizonSource(horizon, svcMetrics)}
	if len(cfg.Networks) > 0 {
		sources = append(sources, newXoxnoSource(cfg.Networks, horizon, svcMetrics))
	}
	return newQuoteService(sources, cfg.SourceTimeout, svcMetrics)
}

// recordCall records one service call. Use it with defer and a named error.
func recordCall(m *metrics.Service, service, method, net string, start time.Time, errp *error) {
	metrics.Record(m, service, method, net, time.Since(start).Seconds(), *errp)
}

// recordQuoteCall records a swap quote call. "No route" and "unsupported" are
// normal answers, not failures, so they do not count as errors.
func recordQuoteCall(m *metrics.Service, method, net string, start time.Time, errp *error) {
	err := *errp
	if errors.Is(err, types.ErrSwapNoRoute) || errors.Is(err, errUnsupported) {
		err = nil
	}
	recordCall(m, quoteServiceName, method, net, start, &err)
}
