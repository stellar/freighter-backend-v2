// ABOUTME: Response types for the wallet-backend-fronted /api/v1/accounts/balances endpoint.
// ABOUTME: Defines the per-account fan-out result shape returned to API consumers.
package types

import (
	"time"

	wbtypes "github.com/stellar/wallet-backend/pkg/wbclient/types"
)

// AccountBalances is the per-account fan-out result returned by the multi-account
// balances endpoint. The wallet-backend GraphQL API exposes balances only via the
// single-account accountByAddress query; freighter-backend issues one such query
// per requested address concurrently and aggregates the results into a slice of
// AccountBalances values, one per unique input address (duplicates are collapsed
// while preserving first-seen order).
//
// Wire format: address is the canonical Stellar account ID, balances is always a
// non-nil slice (an account with no balances marshals to "balances": []), and
// error — when present — carries the wallet-backend account-not-found message
// for that address (the typed wbclient.ErrAccountNotFound sentinel surfaced as
// accountByAddress:null upstream). This is the only address-scoped failure.
// Every other failure (GraphQL errors[] from the server, HTTP 4xx/5xx,
// transport, signing, request-level cancellation) surfaces as a top-level
// error from the service rather than a per-account Error string, so
// monitoring sees real outages instead of a 200 of error strings.
type AccountBalances struct {
	Address  string            `json:"address"`
	Balances []wbtypes.Balance `json:"balances"`
	Error    *string           `json:"error,omitempty"`
}

// PaginationDirection selects forward (next) or backward (prev) traversal of
// a cursor-paginated upstream resource.
type PaginationDirection string

const (
	PaginationDirectionNext PaginationDirection = "next"
	PaginationDirectionPrev PaginationDirection = "prev"
)

// AccountHistoryParams carries pagination and time-range filters for the
// account-scoped transactions history endpoint. Cursor is opaque (forwarded
// verbatim to wallet-backend). All time pointers are nil when the caller omits
// the corresponding query param.
type AccountHistoryParams struct {
	Limit     int32
	Cursor    *string
	Direction PaginationDirection
	Since     *time.Time
	Until     *time.Time
}

// PaginationInfo is the cursor-pagination metadata returned alongside a page
// of items. NextCursor / PrevCursor are nil when HasNext / HasPrevious is
// false respectively.
type PaginationInfo struct {
	NextCursor  *string `json:"next_cursor"`
	PrevCursor  *string `json:"prev_cursor"`
	HasNext     bool    `json:"has_next"`
	HasPrevious bool    `json:"has_previous"`
}

// PaginatedResponse is the generic response envelope for cursor-paginated
// list endpoints. Data is always a non-nil slice (empty when no items).
// This envelope is written directly as the response body — it is NOT wrapped
// by handlers.HttpResponse, because doing so would nest data twice.
type PaginatedResponse[T any] struct {
	Data       []T            `json:"data"`
	Pagination PaginationInfo `json:"pagination"`
}
