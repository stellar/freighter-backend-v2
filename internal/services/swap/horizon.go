package swap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	xoxno "github.com/xoxno/sdk-go"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	hProtocol "github.com/stellar/go-stellar-sdk/protocols/horizon"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const (
	horizonHTTPTimeout = 10 * time.Second
	// maxResponseBytes caps every upstream body read while building a quote.
	maxResponseBytes = 1 << 20
)

var (
	// errHorizonAccountNotFound is returned for an unfunded account.
	errHorizonAccountNotFound = errors.New("account not found")
)

// horizonClient is the slice of Horizon the swap quote needs: the classic
// strict-send and strict-receive path finders, and the account lookup (sequence
// and balances).
type horizonClient struct {
	// bases maps a network to its Horizon URL. The default URL ends in a slash;
	// paths are joined with their own, so it is trimmed.
	bases      map[string]string
	httpClient *http.Client
}

func newHorizonClient(pubnetURL, testnetURL string) *horizonClient {
	bases := map[string]string{}
	for net, u := range map[string]string{types.PUBLIC: pubnetURL, types.TESTNET: testnetURL} {
		if u != "" {
			bases[net] = strings.TrimRight(u, "/")
		}
	}
	return &horizonClient{bases: bases, httpClient: &http.Client{Timeout: horizonHTTPTimeout}}
}

func (c *horizonClient) baseURL(net string) (string, error) {
	base, ok := c.bases[net]
	if !ok {
		return "", fmt.Errorf("%w: %s", errUnsupported, net)
	}
	return base, nil
}

// getJSON GETs reqURL and decodes a 200 body into dest. Any other status
// is returned for the caller to map; its body is drained so the connection is
// reused.
func getJSON(ctx context.Context, client *http.Client, reqURL string, dest any, headers ...http.Header) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("building swap source request: %w", err)
	}
	if len(headers) > 0 {
		req.Header = headers[0].Clone()
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	defer resp.Body.Close() //nolint:errcheck

	body := io.LimitReader(resp.Body, maxResponseBytes)
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, body)
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(body).Decode(dest); err != nil {
		return resp.StatusCode, fmt.Errorf("decoding swap source response: %w", err)
	}
	return resp.StatusCode, nil
}

func statusError(what string, status int) error {
	return &metrics.UpstreamError{Kind: "http_error", Code: status, Err: fmt.Errorf("%s status %d", what, status)}
}

// assetParams sets the type, code and issuer query parameters of a classic
// asset under prefix. A native asset has no code or issuer.
func assetParams(q url.Values, prefix string, a asset) {
	var assetType, code, issuer string
	a.classic.MustExtract(&assetType, &code, &issuer)
	q.Set(prefix+"_type", assetType)
	if !a.classic.IsNative() {
		q.Set(prefix+"_code", code)
		q.Set(prefix+"_issuer", issuer)
	}
}

// strictSendBest returns the best path for a fixed source amount, or nil when
// Horizon has none.
func (c *horizonClient) strictSendBest(ctx context.Context, net string, src, dst asset, sourceAmount string) (*hProtocol.Path, error) {
	q := url.Values{
		"source_amount":      {sourceAmount},
		"destination_assets": {dst.classic.StringCanonical()},
		"limit":              {"1"},
	}
	assetParams(q, "source_asset", src)
	return c.bestPath(ctx, net, "strict-send", q)
}

// strictReceiveBest returns the cheapest path that delivers a fixed destination
// amount, or nil when Horizon has none.
func (c *horizonClient) strictReceiveBest(ctx context.Context, net string, src, dst asset, destAmount string) (*hProtocol.Path, error) {
	q := url.Values{
		"destination_amount": {destAmount},
		"source_assets":      {src.classic.StringCanonical()},
	}
	assetParams(q, "destination_asset", dst)
	return c.bestPath(ctx, net, "strict-receive", q)
}

// bestPath returns the first record Horizon lists for a path-finding endpoint,
// which is the best one.
func (c *horizonClient) bestPath(ctx context.Context, net, endpoint string, q url.Values) (*hProtocol.Path, error) {
	base, err := c.baseURL(net)
	if err != nil {
		return nil, err
	}
	var page hProtocol.PathsPage
	status, err := getJSON(ctx, c.httpClient, base+"/paths/"+endpoint+"?"+q.Encode(), &page)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, statusError("horizon "+endpoint, status)
	}
	if len(page.Embedded.Records) == 0 {
		return nil, nil
	}
	return &page.Embedded.Records[0], nil
}

// holdsAsset reports whether the account can receive the classic asset: native
// XLM always, any other asset only through a trustline.
func holdsAsset(a *hProtocol.Account, target asset) bool {
	if target.classic.IsNative() {
		return true
	}
	want := target.classic.StringCanonical()
	for _, b := range a.Balances {
		if b.Code+":"+b.Issuer == want {
			return true
		}
	}
	return false
}

func (c *horizonClient) account(ctx context.Context, net, address string) (*hProtocol.Account, error) {
	base, err := c.baseURL(net)
	if err != nil {
		return nil, err
	}
	var acct hProtocol.Account
	status, err := getJSON(ctx, c.httpClient, base+"/accounts/"+url.PathEscape(address), &acct)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errHorizonAccountNotFound
	default:
		return nil, statusError("horizon account", status)
	}
	if acct.Sequence < 0 {
		return nil, fmt.Errorf("horizon returned invalid sequence %d", acct.Sequence)
	}
	return &acct, nil
}

// horizonSource quotes through Stellar's built-in DEX and liquidity pools,
// the route the wallet has always offered.
type horizonSource struct {
	client     *horizonClient
	svcMetrics *metrics.Service
}

func newHorizonSource(client *horizonClient, svcMetrics *metrics.Service) *horizonSource {
	return &horizonSource{client: client, svcMetrics: svcMetrics}
}

func (s *horizonSource) Name() string { return types.SwapSourceHorizon }

func (s *horizonSource) Quote(ctx context.Context, req types.SwapQuoteRequest) (_ *candidate, err error) {
	defer recordQuoteCall(s.svcMetrics, types.SwapSourceHorizon, req.Network, time.Now(), &err)

	src, dst, err := classicPair(req)
	if err != nil {
		return nil, err
	}

	rec, err := s.client.strictSendBest(ctx, req.Network, src, dst, req.SourceAmount)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, types.ErrSwapNoRoute
	}
	dest, err := horizonAtoms("destination amount", rec.DestinationAmount)
	if err != nil {
		return nil, err
	}

	path := make([]string, 0, len(rec.Path))
	for _, p := range rec.Path {
		if p.Type == "native" {
			path = append(path, "native")
			continue
		}
		path = append(path, p.Code+":"+p.Issuer)
	}

	return &candidate{
		Source:        types.SwapSourceHorizon,
		DestAmount:    dest,
		DestAmountMin: minAmountOut(dest, req.SlippagePercent),
		DestDecimals:  types.ClassicDecimals,
		Path:          path,
	}, nil
}

// QuoteInput asks Horizon what it takes to receive the requested amount.
func (s *horizonSource) QuoteInput(ctx context.Context, req types.SwapQuoteRequest) (_ *big.Int, err error) {
	defer recordQuoteCall(s.svcMetrics, types.SwapSourceHorizon+"_input", req.Network, time.Now(), &err)

	src, dst, err := classicPair(req)
	if err != nil {
		return nil, err
	}
	rec, err := s.client.strictReceiveBest(ctx, req.Network, src, dst, req.DestAmount)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, types.ErrSwapNoRoute
	}
	return horizonAtoms("source amount", rec.SourceAmount)
}

// classicPair resolves the request's assets for the classic DEX, which only
// trades classic assets at their fixed 7 decimals.
func classicPair(req types.SwapQuoteRequest) (src, dst asset, err error) {
	if src, err = parseAsset(req.SourceAsset); err != nil {
		return asset{}, asset{}, err
	}
	if dst, err = parseAsset(req.DestAsset); err != nil {
		return asset{}, asset{}, err
	}
	if !src.isClassic() || !dst.isClassic() || req.SourceDecimals != types.ClassicDecimals || (req.DestAmount != "" && req.DestDecimals != types.ClassicDecimals) {
		return asset{}, asset{}, errUnsupported
	}
	return src, dst, nil
}

// horizonAtoms parses one of Horizon's fixed 7-decimal amounts; a zero amount
// is "no route".
func horizonAtoms(what, s string) (*big.Int, error) {
	if s == "0" || s == "0.0000000" {
		return nil, types.ErrSwapNoRoute
	}
	v, err := xoxno.ParseDecimalAmount(s, types.ClassicDecimals)
	if err != nil {
		return nil, fmt.Errorf("%w: horizon %s: %v", errInvalidQuote, what, err)
	}
	return v, nil
}
