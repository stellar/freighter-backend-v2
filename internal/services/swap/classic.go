package swap

import (
	"context"
	"encoding/json"
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
)

const classicName = "classic"

type ClassicConfig struct {
	HorizonURLs map[string]string // network → base URL
	HTTPClient  *http.Client
}

type ClassicSource struct {
	urls map[string]string
	hc   *http.Client
	rpc  types.RPCService
}

func NewClassicSource(cfg ClassicConfig, rpc types.RPCService) *ClassicSource {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &ClassicSource{urls: cfg.HorizonURLs, hc: hc, rpc: rpc}
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
	chain := []string{from.ID}
	for _, a := range best.Path {
		if a.Type == "native" {
			chain = append(chain, "XLM")
		} else {
			chain = append(chain, a.Code+":"+a.Issuer)
		}
	}
	chain = append(chain, to.ID)
	hops := make([]types.SwapHop, 0, len(chain)-1)
	for i := 0; i+1 < len(chain); i++ {
		hops = append(hops, types.SwapHop{Venue: "stellar-dex", From: chain[i], To: chain[i+1]})
	}
	// The fixed side echoes the request; Horizon pads it to 7 decimals.
	fromAmt, toAmt := req.Amount, best.DestinationAmount
	if req.Mode == types.SwapModeReceive {
		fromAmt, toAmt = best.SourceAmount, req.Amount
	}
	return &types.SwapRoute{
		Source: classicName, FromAmount: fromAmt, ToAmount: toAmt,
		Paths: []types.SwapPath{{Share: "1", Hops: hops}}, Fees: []types.SwapFee{},
	}, nil
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
