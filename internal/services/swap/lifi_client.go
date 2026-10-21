package swap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
)

const LifiStellarChainID = "1201081091099710"

const maxTokenDecimals = 18

type LifiClient struct {
	base     string
	apiKey   string
	hc       *http.Client
	decimals sync.Map // contract → int
}

func NewLifiClient(baseURL, apiKey string, hc *http.Client) *LifiClient {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
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
		} `json:"feeCosts"`
	} `json:"estimate"`
	TransactionRequest *struct {
		Data string `json:"data"`
	} `json:"transactionRequest"`
}

func (c *LifiClient) Quote(ctx context.Context, fromContract, toContract, rawAmount, fromAddress string) (*LifiQuote, error) {
	q := url.Values{}
	q.Set("fromChain", LifiStellarChainID)
	q.Set("toChain", LifiStellarChainID)
	q.Set("fromToken", fromContract)
	q.Set("toToken", toContract)
	q.Set("fromAmount", rawAmount)
	q.Set("fromAddress", fromAddress)
	q.Set("slippage", "0.005")
	var out LifiQuote
	if err := c.getJSON(ctx, "/v1/quote", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *LifiClient) TokenDecimals(ctx context.Context, contract string) (int, error) {
	if v, ok := c.decimals.Load(contract); ok {
		return v.(int), nil
	}
	q := url.Values{}
	q.Set("chain", LifiStellarChainID)
	q.Set("token", contract)
	var tok struct {
		Decimals int `json:"decimals"`
	}
	if err := c.getJSON(ctx, "/v1/token", q, &tok); err != nil {
		return 0, err
	}
	// Checked before caching: ParseUnits/FormatUnits size strings by decimals.
	if tok.Decimals < 0 || tok.Decimals > maxTokenDecimals {
		return 0, fmt.Errorf("lifi: token %s decimals %d out of range", contract, tok.Decimals)
	}
	c.decimals.Store(contract, tok.Decimals)
	return tok.Decimals, nil
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
		return &metrics.UpstreamError{Kind: "http_error", Err: err}
	}
	defer resp.Body.Close()              //nolint:errcheck
	defer io.Copy(io.Discard, resp.Body) //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusOK:
		return json.NewDecoder(resp.Body).Decode(dest)
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case http.StatusNotFound:
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.NewDecoder(resp.Body).Decode(&e) == nil {
			switch e.Code {
			case 1002, 1003, 1011:
				return ErrNoRoute
			}
		}
	}
	return &metrics.UpstreamError{Kind: "http_error", Code: resp.StatusCode, Err: fmt.Errorf("lifi %s: %d", path, resp.StatusCode)}
}
