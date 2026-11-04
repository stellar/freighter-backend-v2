package types

import "context"

const (
	SwapModeSend    = "send"
	SwapModeReceive = "receive"
)

// SwapQuoteRequest is a validated POST /api/v1/swap/quotes body plus the network.
type SwapQuoteRequest struct {
	Network   string `json:"-"`
	Account   string `json:"account"`
	Mode      string `json:"mode"`
	FromAsset string `json:"fromAsset"`
	ToAsset   string `json:"toAsset"`
	Amount    string `json:"amount"`
}

type SwapHop struct {
	Venue string `json:"venue"`
	From  string `json:"from"`
	To    string `json:"to"`
	Pool  string `json:"pool,omitempty"`
}

type SwapPath struct {
	Share string    `json:"share"`
	Hops  []SwapHop `json:"hops"`
}

type SwapFee struct {
	Name   string `json:"name"`
	Amount string `json:"amount"`
	Asset  string `json:"asset"`
}

type SwapRoute struct {
	Source     string     `json:"source"`
	FromAmount string     `json:"fromAmount"`
	ToAmount   string     `json:"toAmount"`
	Paths      []SwapPath `json:"paths"`
	Fees       []SwapFee  `json:"fees"`
}

type SwapUnavailable struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

type SwapQuotesResponse struct {
	Routes      []SwapRoute       `json:"routes"`
	Unavailable []SwapUnavailable `json:"unavailable"`
}

// SwapBuildRequest is a validated POST /api/v1/swap/tx body plus the network.
// Fee is the user's network fee setting in XLM, as Send uses it.
type SwapBuildRequest struct {
	Network   string     `json:"-"`
	Account   string     `json:"account"`
	Source    string     `json:"source"`
	FromAsset string     `json:"fromAsset"`
	ToAsset   string     `json:"toAsset"`
	Amount    string     `json:"amount"`
	MinOut    string     `json:"minOut"`
	Fee       string     `json:"fee"`
	Paths     []SwapPath `json:"paths,omitempty"`
	// Slippage is the user's tolerance in percent, 0-10 (e.g. "0.5"), required;
	// only the LI.FI source reads it.
	Slippage string `json:"slippage"`
	// Timeout is the user's transaction timeout in seconds; nil means the default.
	// A pointer so an explicit 0 (no expiry) is rejected, not defaulted.
	Timeout *int64 `json:"timeout,omitempty"`
}

type SwapBuildResponse struct {
	XDR           string `json:"xdr"`
	AddsTrustline bool   `json:"addsTrustline"`
}

type SwapService interface {
	Quote(ctx context.Context, req SwapQuoteRequest) (SwapQuotesResponse, error)
	Build(ctx context.Context, req SwapBuildRequest) (SwapBuildResponse, error)
}
