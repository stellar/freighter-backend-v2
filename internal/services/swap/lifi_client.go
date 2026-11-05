package swap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const LifiStellarChainID = "1201081091099710"

// maxUpstreamBody caps how much of a LI.FI or Horizon response is read.
const maxUpstreamBody = 1 << 20

type LifiClient struct {
	base   string
	apiKey string
	hc     *http.Client
}

func NewLifiClient(baseURL, apiKey string, hc *http.Client) *LifiClient {
	if hc == nil {
		hc = utils.NewUpstreamHTTPClient()
	}
	if hc.CheckRedirect == nil {
		// Go forwards custom headers such as x-lifi-api-key on redirects.
		cp := *hc
		cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		hc = &cp
	}
	return &LifiClient{base: strings.TrimRight(baseURL, "/"), apiKey: apiKey, hc: hc}
}

type LifiQuote struct {
	Action struct {
		FromAmount string `json:"fromAmount"`
		FromToken  struct {
			Decimals int `json:"decimals"`
		} `json:"fromToken"`
		ToToken struct {
			Decimals int `json:"decimals"`
		} `json:"toToken"`
	} `json:"action"`
	Estimate struct {
		ToAmount    string `json:"toAmount"`
		ToAmountMin string `json:"toAmountMin"`
		FeeCosts    []struct {
			Name   string `json:"name"`
			Amount string `json:"amount"`
			Token  struct {
				Address string `json:"address"`
			} `json:"token"`
		} `json:"feeCosts"`
	} `json:"estimate"`
	TransactionRequest *struct {
		Data string `json:"data"`
	} `json:"transactionRequest"`
}

func (c *LifiClient) Quote(ctx context.Context, fromContract, toContract, rawAmount, fromAddress, slippage string) (*LifiQuote, error) {
	q := url.Values{}
	q.Set("fromChain", LifiStellarChainID)
	q.Set("toChain", LifiStellarChainID)
	q.Set("fromToken", fromContract)
	q.Set("toToken", toContract)
	q.Set("fromAmount", rawAmount)
	q.Set("fromAddress", fromAddress)
	q.Set("slippage", slippage)
	var out LifiQuote
	if err := c.getJSON(ctx, "/v1/quote", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *LifiClient) getJSON(ctx context.Context, path string, q url.Values, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("x-lifi-api-key", c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// Drop the URL: the quote query carries the user's account.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = fmt.Errorf("lifi %s: %w", path, ue.Err)
		}
		return &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	defer resp.Body.Close() //nolint:errcheck
	body := io.LimitReader(resp.Body, maxUpstreamBody)
	defer io.Copy(io.Discard, body) //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(body).Decode(dest); err != nil {
			return &metrics.UpstreamError{Kind: "invalid_response", Err: fmt.Errorf("lifi %s: %w", path, err)}
		}
		return nil
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case http.StatusNotFound:
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.NewDecoder(body).Decode(&e) == nil {
			switch e.Code {
			case 1002, 1003, 1011:
				return ErrNoRoute
			}
		}
	}
	return &metrics.UpstreamError{Kind: "http_error", Code: resp.StatusCode, Err: fmt.Errorf("lifi %s: %d", path, resp.StatusCode)}
}
