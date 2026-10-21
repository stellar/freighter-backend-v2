package swap

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

var ErrAllSourcesFailed = errors.New("all swap sources failed")

var errMalformedRoute = errors.New("source returned a malformed route")

type Router struct {
	sources []Source
	timeout time.Duration
	m       *metrics.Swap
}

func NewRouter(sources []Source, timeout time.Duration, m *metrics.Swap) *Router {
	return &Router{sources: sources, timeout: timeout, m: m}
}

type result struct {
	src   Source
	route *types.SwapRoute
	err   error
}

func (r *Router) Quote(ctx context.Context, req types.SwapQuoteRequest) (types.SwapQuotesResponse, error) {
	out := types.SwapQuotesResponse{Routes: []types.SwapRoute{}, Unavailable: []types.SwapUnavailable{}}
	var called []Source
	for _, s := range r.sources {
		if !s.Supports(req.Network) {
			out.Unavailable = append(out.Unavailable, types.SwapUnavailable{Source: s.Name(), Reason: "unsupported_network"})
			continue
		}
		called = append(called, s)
	}

	results := make([]result, len(called))
	var wg sync.WaitGroup
	for i, s := range called {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			rt, err := s.Quote(sctx, req)
			if err == nil && sctx.Err() != nil {
				err = sctx.Err()
			}
			results[i] = result{src: s, route: rt, err: err}
		}()
	}
	wg.Wait()
	// The client went away: not a source failure, so nothing to record.
	if err := ctx.Err(); err != nil {
		return out, err
	}

	failed := 0
	for _, res := range results {
		if errors.Is(res.err, ErrUnsupportedMode) {
			continue
		}
		if res.err == nil && !validRoute(req, res.route) {
			res.err = errMalformedRoute
		}
		reason := Reason(res.err)
		metrics.RecordSwapSource(r.m, res.src.Name(), "quote", reason)
		if res.err != nil {
			out.Unavailable = append(out.Unavailable, types.SwapUnavailable{Source: res.src.Name(), Reason: reason})
			if reason == "timeout" || reason == "rate_limited" || reason == "error" {
				failed++
			}
			continue
		}
		out.Routes = append(out.Routes, *res.route)
	}

	if len(out.Routes) == 0 && failed > 0 && failed == countAsked(results) {
		return out, ErrAllSourcesFailed
	}
	r.rank(req, out.Routes)
	return out, nil
}

func countAsked(results []result) int {
	n := 0
	for _, res := range results {
		if !errors.Is(res.err, ErrUnsupportedMode) {
			n++
		}
	}
	return n
}

// validRoute reports whether rt is non-nil and its ranking amount parses;
// rat() maps garbage to 0, which would let a bad route win in receive mode.
func validRoute(req types.SwapQuoteRequest, rt *types.SwapRoute) bool {
	if rt == nil {
		return false
	}
	amt := rt.ToAmount
	if req.Mode == types.SwapModeReceive {
		amt = rt.FromAmount
	}
	_, ok := new(big.Rat).SetString(amt)
	return ok
}

func rat(s string) *big.Rat {
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return new(big.Rat)
	}
	return v
}

func (r *Router) rank(req types.SwapQuoteRequest, routes []types.SwapRoute) {
	send := req.Mode != types.SwapModeReceive
	key := func(rt types.SwapRoute) *big.Rat {
		if send {
			return rat(rt.ToAmount)
		}
		return rat(rt.FromAmount)
	}
	sort.SliceStable(routes, func(i, j int) bool {
		c := key(routes[i]).Cmp(key(routes[j]))
		if send {
			return c > 0
		}
		return c < 0
	})
	if len(routes) < 2 {
		return
	}
	best, second := key(routes[0]), key(routes[1])
	if second.Sign() == 0 {
		return
	}
	diff := new(big.Rat).Sub(best, second)
	if !send {
		diff.Neg(diff)
	}
	bps, _ := new(big.Rat).Mul(new(big.Rat).Quo(diff, second), big.NewRat(10000, 1)).Float64()
	metrics.RecordSwapWinner(r.m, routes[0].Source, pairKind(req), bps)
}

func pairKind(req types.SwapQuoteRequest) string {
	from, err1 := ParseAsset(req.FromAsset)
	to, err2 := ParseAsset(req.ToAsset)
	if err1 == nil && err2 == nil && from.IsClassic() && to.IsClassic() {
		return "classic"
	}
	return "soroban"
}

func (r *Router) Build(ctx context.Context, req types.SwapBuildRequest) (types.SwapBuildResponse, error) {
	for _, s := range r.sources {
		if s.Name() != req.Source {
			continue
		}
		if !s.Supports(req.Network) {
			return types.SwapBuildResponse{}, fmt.Errorf("%w: source %s does not support %s", ErrBadRequest, req.Source, req.Network)
		}
		x, err := s.BuildTx(ctx, req)
		if cerr := ctx.Err(); cerr != nil {
			return types.SwapBuildResponse{}, cerr
		}
		metrics.RecordSwapSource(r.m, s.Name(), "build", Reason(err))
		if err != nil {
			return types.SwapBuildResponse{}, err
		}
		toContract := ""
		if to, perr := ParseAsset(req.ToAsset); perr == nil {
			toContract, _ = to.ContractID(req.Network)
		}
		adds, err := AddsTrustline(x, req.Account, toContract)
		if err != nil {
			return types.SwapBuildResponse{}, err
		}
		return types.SwapBuildResponse{XDR: x, AddsTrustline: adds}, nil
	}
	return types.SwapBuildResponse{}, fmt.Errorf("%w: unknown source %q", ErrBadRequest, req.Source)
}
