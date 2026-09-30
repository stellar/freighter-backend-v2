package swap

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	xoxno "github.com/xoxno/sdk-go"
)

const xoxnoHTTPTimeout = 15 * time.Second

type Network struct{ QuoteURL, Router string }
type xoxnoSource struct {
	networks   map[string]Network
	accounts   *horizonClient
	httpClient *http.Client
	now        func() time.Time
	svcMetrics *metrics.Service
}

func newXoxnoSource(n map[string]Network, a *horizonClient, m *metrics.Service) *xoxnoSource {
	return &xoxnoSource{n, a, &http.Client{Timeout: xoxnoHTTPTimeout}, time.Now, m}
}
func (s *xoxnoSource) Name() string { return types.SwapSourceXoxno }
func (s *xoxnoSource) client(req types.SwapQuoteRequest) (*xoxno.Client, xoxno.QuoteRequest, asset, error) {
	cfg, ok := s.networks[req.Network]
	if !ok || cfg.QuoteURL == "" || cfg.Router == "" {
		return nil, xoxno.QuoteRequest{}, asset{}, errUnsupported
	}
	pass, e := networkPassphrase(req.Network)
	if e != nil {
		return nil, xoxno.QuoteRequest{}, asset{}, e
	}
	src, e := parseAsset(req.SourceAsset)
	if e != nil {
		return nil, xoxno.QuoteRequest{}, asset{}, e
	}
	dst, e := parseAsset(req.DestAsset)
	if e != nil {
		return nil, xoxno.QuoteRequest{}, asset{}, e
	}
	in, e := src.contractIDFor(pass)
	if e != nil {
		return nil, xoxno.QuoteRequest{}, asset{}, e
	}
	out, e := dst.contractIDFor(pass)
	if e != nil {
		return nil, xoxno.QuoteRequest{}, asset{}, e
	}
	c := xoxno.NewClient(xoxno.Config{QuoteURL: cfg.QuoteURL, Router: cfg.Router, NetworkPassphrase: pass, HTTPClient: s.httpClient, Now: s.now})
	r := xoxno.QuoteRequest{SourceToken: in, DestToken: out, DestClassic: dst.isClassic(), SourceAmount: req.SourceAmount, DestAmount: req.DestAmount, SourceDecimals: req.SourceDecimals, DestDecimals: req.DestDecimals, Sender: req.Sender, SlippagePercent: req.SlippagePercent, TimeoutSeconds: req.TimeoutSeconds}
	return c, r, dst, nil
}
func (s *xoxnoSource) Quote(ctx context.Context, req types.SwapQuoteRequest) (_ *candidate, err error) {
	defer recordQuoteCall(s.svcMetrics, types.SwapSourceXoxno, req.Network, time.Now(), &err)
	c, r, dst, e := s.client(req)
	if e != nil {
		return nil, e
	}
	acct, e := s.accounts.account(ctx, req.Network, req.Sender)
	if errors.Is(e, errHorizonAccountNotFound) {
		return nil, errUnsupported
	}
	if e != nil {
		return nil, e
	}
	r.AccountSequence = acct.Sequence
	r.PrepareTransaction = !(dst.isClassic() && !holdsAsset(acct, dst))
	q, e := c.Quote(ctx, r)
	if e != nil {
		return nil, xoxnoError(e)
	}
	return &candidate{Source: s.Name(), DestAmount: q.DestAmount, DestAmountMin: q.DestAmountMin, DestDecimals: q.DestDecimals, Route: q.Route, Transaction: q.Transaction, PriceImpact: q.PriceImpact, RequiresTrustline: !r.PrepareTransaction, NetworkFee: q.NetworkFee}, nil
}
func (s *xoxnoSource) QuoteInput(ctx context.Context, req types.SwapQuoteRequest) (_ *big.Int, err error) {
	defer recordQuoteCall(s.svcMetrics, types.SwapSourceXoxno+"_input", req.Network, time.Now(), &err)
	c, r, _, e := s.client(req)
	if e != nil {
		return nil, e
	}
	out, e := c.QuoteInput(ctx, r)
	return out, xoxnoError(e)
}
func xoxnoError(e error) error {
	var upstream *xoxno.HTTPError
	if errors.As(e, &upstream) {
		return &metrics.UpstreamError{Kind: "http_error", Code: upstream.Code, Err: upstream.Err}
	}
	switch {
	case errors.Is(e, xoxno.ErrNoRoute):
		return types.ErrSwapNoRoute
	case errors.Is(e, xoxno.ErrUnsupported):
		return errUnsupported
	case errors.Is(e, xoxno.ErrInvalidQuote):
		return fmt.Errorf("%w: %v", errInvalidQuote, e)
	}
	return e
}
