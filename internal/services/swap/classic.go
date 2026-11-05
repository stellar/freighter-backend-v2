package swap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const classicName = "classic"

// orderbookTimeout caps the sdex-orderbook call; it also never takes more
// than half the caller's remaining budget, so Horizon keeps time to answer.
const orderbookTimeout = time.Second

type ClassicConfig struct {
	HorizonURLs   map[string]string // network → base URL
	OrderbookURLs map[string]string // network → sdex-orderbook base URL
	HTTPClient    *http.Client
	Metrics       *metrics.Swap
}

type ClassicSource struct {
	urls      map[string]string
	orderbook map[string]string
	hc        *http.Client
	rpc       types.RPCService
	m         *metrics.Swap
}

func NewClassicSource(cfg ClassicConfig, rpc types.RPCService) *ClassicSource {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = utils.NewUpstreamHTTPClient()
	}
	return &ClassicSource{urls: cfg.HorizonURLs, orderbook: cfg.OrderbookURLs, hc: hc, rpc: rpc, m: cfg.Metrics}
}

func (c *ClassicSource) Name() string { return classicName }

// maxClassicPathHops is five intermediates plus the final hop.
const maxClassicPathHops = 6

func (c *ClassicSource) Supports(req types.SwapQuoteRequest) bool {
	from, err1 := ParseAsset(req.FromAsset)
	to, err2 := ParseAsset(req.ToAsset)
	return c.urls[req.Network] != "" && err1 == nil && err2 == nil && from.IsClassic() && to.IsClassic()
}

type horizonAsset struct {
	Type   string `json:"asset_type"`
	Code   string `json:"asset_code"`
	Issuer string `json:"asset_issuer"`
}

type horizonPath struct {
	SourceAmount      string         `json:"source_amount"`
	DestinationAmount string         `json:"destination_amount"`
	Path              []horizonAsset `json:"path"`
}

func assetParams(prefix string, a Asset) url.Values {
	v := url.Values{}
	if a.IsNative() {
		v.Set(prefix+"asset_type", "native")
		return v
	}
	typ := "credit_alphanum4"
	if len(a.Code) > 4 {
		typ = "credit_alphanum12"
	}
	v.Set(prefix+"asset_type", typ)
	v.Set(prefix+"asset_code", a.Code)
	v.Set(prefix+"asset_issuer", a.Issuer)
	return v
}

func horizonAssetID(a Asset) string {
	if a.IsNative() {
		return "native"
	}
	return a.ID
}

func (c *ClassicSource) Quote(ctx context.Context, req types.SwapQuoteRequest) (*types.SwapRoute, error) {
	from, err := ParseAsset(req.FromAsset)
	if err != nil {
		return nil, err
	}
	to, err := ParseAsset(req.ToAsset)
	if err != nil {
		return nil, err
	}
	if !from.IsClassic() || !to.IsClassic() {
		return nil, fmt.Errorf("%w: classic swaps need classic assets", ErrBadRequest)
	}
	if base := c.orderbook[req.Network]; base != "" {
		rt, err := c.orderbookQuote(ctx, base, req, from, to)
		var fb *orderbookFallback
		if !errors.As(err, &fb) {
			return rt, err
		}
		metrics.RecordSwapOrderbookFallback(c.m, req.Network, fb.reason)
	}
	var endpoint string
	var q url.Values
	if req.Mode == types.SwapModeReceive {
		endpoint = "/paths/strict-receive"
		q = assetParams("destination_", to)
		q.Set("destination_amount", req.Amount)
		q.Set("source_assets", horizonAssetID(from))
	} else {
		endpoint = "/paths/strict-send"
		q = assetParams("source_", from)
		q.Set("source_amount", req.Amount)
		q.Set("destination_assets", horizonAssetID(to))
	}
	var page struct {
		Embedded struct {
			Records []horizonPath `json:"records"`
		} `json:"_embedded"`
	}
	if err := c.getJSON(ctx, req.Network, endpoint, q, &page); err != nil {
		return nil, err
	}
	best := pickPath(page.Embedded.Records, req.Mode == types.SwapModeReceive)
	if best == nil {
		return nil, ErrNoRoute
	}
	path := make([]string, 0, len(best.Path))
	for _, a := range best.Path {
		if a.Type == "native" {
			path = append(path, "XLM")
		} else {
			path = append(path, a.Code+":"+a.Issuer)
		}
	}
	return classicRoute(req, from, to, path, best.SourceAmount, best.DestinationAmount), nil
}

// classicRoute builds a single-path route through the intermediate assets.
func classicRoute(req types.SwapQuoteRequest, from, to Asset, path []string, sourceAmt, destAmt string) *types.SwapRoute {
	chain := append(append([]string{from.ID}, path...), to.ID)
	hops := make([]types.SwapHop, 0, len(chain)-1)
	for i := 0; i+1 < len(chain); i++ {
		hops = append(hops, types.SwapHop{Venue: "stellar-dex", From: chain[i], To: chain[i+1]})
	}
	// The fixed side echoes the request; upstreams pad it to 7 decimals.
	fromAmt, toAmt := req.Amount, destAmt
	if req.Mode == types.SwapModeReceive {
		fromAmt, toAmt = sourceAmt, req.Amount
	}
	return &types.SwapRoute{
		Source: classicName, FromAmount: fromAmt, ToAmount: toAmt,
		Paths: []types.SwapPath{{Share: "1", Hops: hops}}, Fees: []types.SwapFee{},
	}
}

// orderbookFallback means sdex-orderbook gave no answer and Horizon should.
type orderbookFallback struct {
	reason string // not_ready | timeout | error
	err    error
}

func (e *orderbookFallback) Error() string {
	return "sdex-orderbook " + e.reason + ": " + e.err.Error()
}
func (e *orderbookFallback) Unwrap() error { return e.err }

// orderbookQuote asks sdex-orderbook (github.com/stellar/sdex-orderbook).
// Only a 200 with positive amounts and a 404 with code no_route are answers;
// anything else returns *orderbookFallback.
func (c *ClassicSource) orderbookQuote(parent context.Context, base string, req types.SwapQuoteRequest, from, to Asset) (*types.SwapRoute, error) {
	limit := orderbookTimeout
	if dl, ok := parent.Deadline(); ok {
		limit = min(limit, time.Until(dl)/2)
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	// When the caller's own context is done, Horizon has no time left either.
	failed := func(err error) error {
		if parent.Err() != nil {
			return parent.Err()
		}
		if ctx.Err() != nil {
			return &orderbookFallback{"timeout", err}
		}
		return &orderbookFallback{"error", err}
	}
	reqBody, err := json.Marshal(map[string]string{
		"mode": req.Mode, "fromAsset": from.ID, "toAsset": to.ID, "amount": req.Amount,
	})
	if err != nil {
		return nil, &orderbookFallback{"error", err}
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/quote", bytes.NewReader(reqBody))
	if err != nil {
		return nil, &orderbookFallback{"error", err}
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(hreq)
	if err != nil {
		return nil, failed(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body := io.LimitReader(resp.Body, maxUpstreamBody)
	defer io.Copy(io.Discard, body) //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// A 404 from a wrong path or a proxy is not an answer.
		var e struct {
			Code string `json:"code"`
		}
		if derr := json.NewDecoder(body).Decode(&e); derr != nil {
			return nil, failed(derr)
		}
		if e.Code == "no_route" {
			return nil, ErrNoRoute
		}
		return nil, &orderbookFallback{"error", fmt.Errorf("status 404 code %q", e.Code)}
	case http.StatusServiceUnavailable:
		return nil, &orderbookFallback{"not_ready", fmt.Errorf("status %d", resp.StatusCode)}
	case http.StatusGatewayTimeout:
		return nil, &orderbookFallback{"timeout", fmt.Errorf("status %d", resp.StatusCode)}
	default:
		return nil, &orderbookFallback{"error", fmt.Errorf("status %d", resp.StatusCode)}
	}
	var q struct {
		FromAmount string   `json:"fromAmount"`
		ToAmount   string   `json:"toAmount"`
		Path       []string `json:"path"`
	}
	if err = json.NewDecoder(body).Decode(&q); err != nil {
		return nil, failed(err)
	}
	if len(q.Path) > maxClassicPathHops-1 {
		return nil, &orderbookFallback{"error", fmt.Errorf("path of %d assets", len(q.Path))}
	}
	// The route shows req.Amount on the fixed side, so a quote for any other
	// amount is not an answer to this request.
	echo := q.FromAmount
	if req.Mode == types.SwapModeReceive {
		echo = q.ToAmount
	}
	want, _ := ParseUnits(req.Amount, ClassicDecimals)
	if got, perr := ParseUnits(echo, ClassicDecimals); perr != nil || want == nil || got.Cmp(want) != 0 {
		return nil, &orderbookFallback{"error", fmt.Errorf("quoted amount %q for %q", echo, req.Amount)}
	}
	for _, p := range q.Path {
		if a, perr := ParseAsset(p); perr != nil || !a.IsClassic() {
			return nil, &orderbookFallback{"error", fmt.Errorf("path asset %q", p)}
		}
	}
	rt := classicRoute(req, from, to, q.Path, q.FromAmount, q.ToAmount)
	for _, amt := range []string{rt.FromAmount, rt.ToAmount} {
		if v, perr := ParseUnits(amt, ClassicDecimals); perr != nil || v.Sign() <= 0 {
			return nil, &orderbookFallback{"error", fmt.Errorf("amount %q", amt)}
		}
	}
	return rt, nil
}

func pickPath(recs []horizonPath, receive bool) *horizonPath {
	var best *horizonPath
	for i := range recs {
		r := &recs[i]
		if best == nil {
			best = r
			continue
		}
		if receive && rat(r.SourceAmount).Cmp(rat(best.SourceAmount)) < 0 ||
			!receive && rat(r.DestinationAmount).Cmp(rat(best.DestinationAmount)) > 0 {
			best = r
		}
	}
	return best
}

func (c *ClassicSource) getJSON(ctx context.Context, network, path string, q url.Values, dest any) error {
	base := strings.TrimRight(c.urls[network], "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	defer resp.Body.Close() //nolint:errcheck
	body := io.LimitReader(resp.Body, maxUpstreamBody)
	defer io.Copy(io.Discard, body) //nolint:errcheck
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("horizon %s: %w", path, ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return &metrics.UpstreamError{Kind: "http_error", Code: resp.StatusCode, Err: fmt.Errorf("horizon %s: %d", path, resp.StatusCode)}
	}
	return json.NewDecoder(body).Decode(dest)
}

func (c *ClassicSource) BuildTx(ctx context.Context, req types.SwapBuildRequest) (string, error) {
	from, err := ParseAsset(req.FromAsset)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	to, err := ParseAsset(req.ToAsset)
	if err != nil || !from.IsClassic() || !to.IsClassic() {
		return "", fmt.Errorf("%w: classic swaps need classic assets", ErrBadRequest)
	}
	acct, err := xdr.AddressToAccountId(req.Account)
	if err != nil {
		return "", fmt.Errorf("%w: account", ErrBadRequest)
	}
	var acctKey xdr.LedgerKey
	if err = acctKey.SetAccount(acct); err != nil {
		return "", err
	}
	acctKeyB64, err := xdr.MarshalBase64(acctKey)
	if err != nil {
		return "", err
	}
	keys := []string{acctKeyB64}
	var tlKeyB64 string
	// An issuer holds no trustline to its own asset.
	if !to.IsNative() && to.Issuer != req.Account {
		var toX xdr.Asset
		toX, err = to.XDR()
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
		}
		var tlKey xdr.LedgerKey
		if err = tlKey.SetTrustline(acct, toX.ToTrustLineAsset()); err != nil {
			return "", err
		}
		tlKeyB64, err = xdr.MarshalBase64(tlKey)
		if err != nil {
			return "", err
		}
		keys = append(keys, tlKeyB64)
	}
	entries, err := c.rpc.GetLedgerEntriesXDR(ctx, keys, req.Network)
	if err != nil {
		return "", err
	}
	acctData, ok := entries[acctKeyB64]
	if !ok {
		return "", ErrAccountNotFound
	}
	var data xdr.LedgerEntryData
	if err = xdr.SafeUnmarshalBase64(acctData, &data); err != nil {
		return "", err
	}
	seq := int64(data.MustAccount().SeqNum)

	fromT, err := from.TxnbuildAsset()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	toT, err := to.TxnbuildAsset()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	// The build must follow the quoted route, so the path is checked, not
	// trusted. A PathPaymentStrictSendOp path holds at most five assets;
	// txnbuild does not enforce it and a longer path encodes but cannot decode.
	if len(req.Paths) != 1 || len(req.Paths[0].Hops) == 0 || len(req.Paths[0].Hops) > maxClassicPathHops {
		return "", fmt.Errorf("%w: path", ErrBadRequest)
	}
	hops := req.Paths[0].Hops
	if hops[0].From != req.FromAsset || hops[len(hops)-1].To != req.ToAsset {
		return "", fmt.Errorf("%w: path", ErrBadRequest)
	}
	var path []txnbuild.Asset
	for i := 0; i+1 < len(hops); i++ {
		var mid Asset
		mid, err = ParseAsset(hops[i].To)
		if err != nil || !mid.IsClassic() {
			return "", fmt.Errorf("%w: path", ErrBadRequest)
		}
		var midT txnbuild.Asset
		midT, err = mid.TxnbuildAsset()
		if err != nil {
			return "", fmt.Errorf("%w: path", ErrBadRequest)
		}
		path = append(path, midT)
	}
	ops := []txnbuild.Operation{}
	if tlKeyB64 != "" {
		if _, has := entries[tlKeyB64]; !has {
			credit, ok := toT.(txnbuild.CreditAsset)
			if !ok {
				return "", fmt.Errorf("%w: destination asset", ErrBadRequest)
			}
			var line txnbuild.ChangeTrustAsset
			line, err = credit.ToChangeTrustAsset()
			if err != nil {
				return "", err
			}
			ops = append(ops, &txnbuild.ChangeTrust{Line: line, Limit: txnbuild.MaxTrustlineLimit})
		}
	}
	ops = append(ops, &txnbuild.PathPaymentStrictSend{
		SendAsset: fromT, SendAmount: req.Amount, Destination: req.Account,
		DestAsset: toT, DestMin: req.MinOut, Path: path,
	})
	totalFee, err := ParseUnits(req.Fee, ClassicDecimals)
	if err != nil {
		return "", fmt.Errorf("%w: fee", ErrBadRequest)
	}
	perOp := totalFee.Int64() / int64(len(ops))
	if perOp < txnbuild.MinBaseFee {
		perOp = txnbuild.MinBaseFee
	}
	timeout, err := timeoutSeconds(req)
	if err != nil {
		return "", err
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: req.Account, Sequence: seq},
		IncrementSequenceNum: true,
		Operations:           ops,
		BaseFee:              perOp,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(timeout)},
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return tx.Base64()
}
