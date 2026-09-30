package types

import (
	"errors"

	xoxno "github.com/xoxno/sdk-go"
)

// ClassicDecimals is the fixed precision of every classic asset and its Stellar
// Asset Contract.
const ClassicDecimals = 7

// ErrSwapNoRoute means the source answered and found no route for the pair.
var ErrSwapNoRoute = errors.New("no swap route found")

// Swap quote sources. The wire values are part of the client contract: the
// client picks its transaction-building path from SwapQuote.Source.
const (
	SwapSourceHorizon = "horizon"
	SwapSourceXoxno   = "xoxno"
	SwapSourceLifi    = "lifi"
)

// SwapQuoteRequest is a validated request for the best route from SourceAsset
// to DestAsset. Assets are canonical ids ("XLM", "CODE:ISSUER") or Soroban
// contract ids ("C..."). SourceAmount is a decimal string in whole-token units.
type SwapQuoteRequest struct {
	Network        string
	SourceAsset    string
	DestAsset      string
	SourceAmount   string
	SourceDecimals int
	// DestAmount is set instead of SourceAmount when the user typed the amount to
	// receive. The service then sizes the input and fills SourceAmount before it
	// asks for the executable quote.
	DestAmount      string
	DestDecimals    int
	Sender          string
	SlippagePercent float64
	// TimeoutSeconds bounds how long a source-built transaction stays valid.
	TimeoutSeconds int64
}

// SwapRouteHop is one pool crossing in an aggregated route.
type SwapRouteHop = xoxno.RouteHop

// SwapTransaction is an unsigned, source-built transaction the client signs as is.
type SwapTransaction = xoxno.Transaction

// SwapQuoteAlternative reports what a source offered, or why it did not.
type SwapQuoteAlternative struct {
	Source            string `json:"source"`
	DestinationAmount string `json:"destinationAmount,omitempty"`
	Selected          bool   `json:"selected"`
	Error             string `json:"error,omitempty"`
}

// SwapQuote is the winning route plus what the other sources offered.
type SwapQuote struct {
	Source               string           `json:"source"`
	SourceAmount         string           `json:"sourceAmount"`
	DestinationAmount    string           `json:"destinationAmount"`
	DestinationAmountMin string           `json:"destinationAmountMin"`
	DestinationDecimals  int              `json:"destinationDecimals"`
	ConversionRate       string           `json:"conversionRate"`
	Path                 []string         `json:"path,omitempty"`
	Route                []SwapRouteHop   `json:"route,omitempty"`
	Transaction          *SwapTransaction `json:"transaction,omitempty"`
	// NetworkFeeXlm is the full fee, in XLM, of the returned transaction. It is
	// absent when the client builds the transaction and applies its own fee.
	NetworkFeeXlm *string `json:"networkFeeXlm,omitempty"`
	// RequiresTrustline is set when the route has no transaction yet because the
	// sender must first open the destination trustline. Ask again afterwards.
	RequiresTrustline bool                   `json:"requiresTrustline,omitempty"`
	PriceImpact       *float64               `json:"priceImpact,omitempty"`
	Alternatives      []SwapQuoteAlternative `json:"alternatives"`
}
