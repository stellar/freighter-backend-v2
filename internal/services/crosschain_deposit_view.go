// ABOUTME: The composed deposits view for one account: Cross Mesh deposits newest first with user_state,
// ABOUTME: fees attributed across settlement rounds (TR-39) and the amount still needed to settle (TR-49).
package services

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/stellar/freighter-backend-v2/internal/logger"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// crossMeshDepositsPageLimit is the largest page GET /v1/deposits serves.
const crossMeshDepositsPageLimit = 100

// DepositRow is one deposit in the deposits view. Amounts are base-unit
// strings.
type DepositRow struct {
	DepositID     string  `json:"deposit_id"`
	Status        string  `json:"status"`
	HoldReason    *string `json:"hold_reason"`
	HoldDetail    *string `json:"hold_detail"`
	UserState     string  `json:"user_state"`
	Amount        string  `json:"amount"`
	Decimals      int     `json:"decimals"`
	SourceChain   string  `json:"source_chain"`
	SourceChainID int64   `json:"source_chain_id"`
	SourceTx      string  `json:"source_tx"`
	DestinationTx *string `json:"destination_tx"`
	SettlementID  *string `json:"settlement_id"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	// RemainingToMinimum is set only on held below_minimum rows that still
	// need more to settle (TR-49).
	RemainingToMinimum *DepositAmount `json:"remaining_to_minimum"`
	// Fees is set only on the newest deposit of a settlement chain (TR-39).
	Fees *DepositFees `json:"fees"`
}

// DepositAmount is a base-unit amount with its decimals.
type DepositAmount struct {
	Amount   string `json:"amount"`
	Decimals int    `json:"decimals"`
}

// DepositFees are the fees of one settlement chain, summed across its rounds.
// CCTPFee and TotalFee are null until every round has a cctp_fee;
// DeliveredAmount is null until every round has a delivery.
type DepositFees struct {
	SetupFee        string  `json:"setup_fee"`
	SettlementFee   string  `json:"settlement_fee"`
	CCTPFee         *string `json:"cctp_fee"`
	TotalFee        *string `json:"total_fee"`
	DeliveredAmount *string `json:"delivered_amount"`
	Decimals        int     `json:"decimals"`
}

// ListDeposits returns every deposit of account, newest first, with its
// user_state, fees and remaining_to_minimum. Any upstream failure is an
// error (TR-52); the view is never partial.
func (s *CrossChainDepositService) ListDeposits(ctx context.Context, network, account string) ([]DepositRow, error) {
	if !CrossMeshConfigured(s.cfg, network) {
		return nil, ErrCrossChainNotConfigured
	}
	deposits, settlements, err := s.depositsAndSettlements(ctx, network, account)
	if isSettlementNotFound(err) {
		// A deposit recorded late can move to another round between the
		// listing and the fetch; a fresh listing names the round it is in now.
		deposits, settlements, err = s.depositsAndSettlements(ctx, network, account)
	}
	if err != nil {
		return nil, err
	}

	rows := make([]DepositRow, len(deposits))
	for i, d := range deposits {
		rows[i] = DepositRow{
			DepositID: d.DepositID, Status: d.Status, HoldReason: d.HoldReason, HoldDetail: d.HoldDetail,
			UserState: userState(d), Amount: d.Amount, Decimals: d.Decimals,
			SourceChain: d.SourceChain, SourceChainID: d.SourceChainID, SourceTx: d.SourceTx,
			DestinationTx: d.DestinationTx, SettlementID: d.SettlementID,
			CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		}
	}
	if err = attributeFees(rows, settlements); err != nil {
		return nil, err
	}
	if err = s.addRemainingToMinimum(ctx, network, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func isSettlementNotFound(err error) bool {
	var cmErr *CrossMeshError
	return errors.As(err, &cmErr) && cmErr.Code == "settlement_not_found"
}

// depositsAndSettlements lists every deposit of account, newest first, and
// fetches every settlement round they belong to.
func (s *CrossChainDepositService) depositsAndSettlements(ctx context.Context, network, account string) ([]types.CrossMeshDeposit, map[string]*types.CrossMeshSettlement, error) {
	deposits, err := s.listDeposits(ctx, network, account)
	if err != nil {
		return nil, nil, err
	}
	settlements, err := s.settlements(ctx, network, deposits)
	if err != nil {
		return nil, nil, err
	}
	return deposits, settlements, nil
}

// listDeposits follows next_cursor to the last page, then orders the
// deposits by created_at descending, ties broken by deposit_id descending
// (D10). created_at is compared as a time: RFC 3339 strings with different
// fractional-second lengths do not sort as text.
func (s *CrossChainDepositService) listDeposits(ctx context.Context, network, account string) ([]types.CrossMeshDeposit, error) {
	var deposits []types.CrossMeshDeposit
	cursor := ""
	for {
		page, err := s.crossMesh.ListDeposits(ctx, network, account, cursor, crossMeshDepositsPageLimit)
		if err != nil {
			return nil, err
		}
		deposits = append(deposits, page.Data...)
		if !page.HasMore || page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}

	created := make(map[string]time.Time, len(deposits))
	for _, d := range deposits {
		t, err := time.Parse(time.RFC3339Nano, d.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("decoding deposit %s created_at: %w", d.DepositID, err)
		}
		created[d.CreatedAt] = t
	}
	slices.SortStableFunc(deposits, func(a, b types.CrossMeshDeposit) int {
		if c := created[b.CreatedAt].Compare(created[a.CreatedAt]); c != 0 {
			return c
		}
		return strings.Compare(b.DepositID, a.DepositID)
	})
	return deposits, nil
}

// settlements fetches every round the deposits reference, then walks
// continuation_of backwards to the rounds no deposit references. Each id is
// fetched once, so a continuation_of cycle ends.
func (s *CrossChainDepositService) settlements(ctx context.Context, network string, deposits []types.CrossMeshDeposit) (map[string]*types.CrossMeshSettlement, error) {
	// An id is in fetched (as nil) from the moment it is queued.
	fetched := map[string]*types.CrossMeshSettlement{}
	var pending []string
	queue := func(id string) {
		if _, seen := fetched[id]; !seen {
			fetched[id] = nil
			pending = append(pending, id)
		}
	}
	for _, d := range deposits {
		if d.SettlementID != nil {
			queue(*d.SettlementID)
		}
	}

	for len(pending) > 0 {
		batch := pending
		pending = nil
		results := make([]*types.CrossMeshSettlement, len(batch))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(CrossMeshSettlementConcurrency)
		for i, id := range batch {
			g.Go(func() error {
				st, err := s.settlement(gctx, network, id)
				results[i] = st
				return err
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		for i, id := range batch {
			fetched[id] = results[i]
			if prev := results[i].ContinuationOf; prev != nil {
				queue(*prev)
			}
		}
	}
	return fetched, nil
}

func settlementCacheKey(network, id string) string {
	return "crossmesh:settlement:v1:" + network + ":" + id
}

// settlement fetches one settlement round. A completed round (delivery and
// cctp_fee both set, so nothing in it changes again) is cached for
// CrossMeshSettlementCacheTTL as soon as it arrives (D9); a cache failure
// falls through to Cross Mesh.
func (s *CrossChainDepositService) settlement(ctx context.Context, network, id string) (*types.CrossMeshSettlement, error) {
	key := settlementCacheKey(network, id)
	if s.cache != nil {
		hits, err := s.cache.MGetJSON(ctx, []string{key}, func() any { return new(types.CrossMeshSettlement) })
		if err != nil {
			logger.Warn("crosschain: redis MGet failed; bypassing cache", "network", network, "error", err)
		} else if hit, ok := hits[key].(*types.CrossMeshSettlement); ok {
			return hit, nil
		}
	}

	st, err := s.crossMesh.GetSettlement(ctx, network, id)
	if err != nil {
		return nil, err
	}
	if s.cache != nil && st.Delivery != nil && st.Fees != nil && st.Fees.CCTPFee != nil {
		wctx, cancel := cacheWriteContext(ctx)
		defer cancel()
		if err = s.cache.SetJSON(wctx, key, st, CrossMeshSettlementCacheTTL); err != nil {
			logger.Warn("crosschain: redis SET failed", "network", network, "error", err)
		}
	}
	return st, nil
}

// userState maps a deposit to what the user is shown (TR-36/37). Unknown
// statuses and hold reasons show as pending, as Cross Mesh asks.
func userState(d types.CrossMeshDeposit) string {
	reason := ""
	if d.HoldReason != nil {
		reason = *d.HoldReason
	}
	switch {
	case d.Status == "completed":
		return "completed"
	case d.Status == "held" && (reason == "below_minimum" || reason == "recipient_not_ready"):
		return "action_needed"
	case d.Status == "held" && reason == "external_settlement_unverified":
		return "under_review"
	default:
		return "pending"
	}
}

// attributeFees puts each settlement chain's fees on its newest deposit
// (TR-39). A chain is the set of rounds linked by continuation_of. rows must
// be newest first and every settlement id in them must be in settlements.
func attributeFees(rows []DepositRow, settlements map[string]*types.CrossMeshSettlement) error {
	// Union each round with the round it continues; a chain's rounds share a
	// root.
	parent := make(map[string]string, len(settlements))
	for id := range settlements {
		parent[id] = id
	}
	root := func(id string) string {
		for parent[id] != id {
			id = parent[id]
		}
		return id
	}
	for id, st := range settlements {
		if st.ContinuationOf != nil {
			if a, b := root(id), root(*st.ContinuationOf); a != b {
				parent[a] = b
			}
		}
	}
	rounds := map[string][]*types.CrossMeshSettlement{}
	for id, st := range settlements {
		rounds[root(id)] = append(rounds[root(id)], st)
	}

	owned := map[string]bool{}
	for i := range rows {
		if rows[i].SettlementID == nil {
			continue
		}
		chain := root(*rows[i].SettlementID)
		if owned[chain] {
			continue
		}
		owned[chain] = true
		fees, err := chainFees(rounds[chain])
		if err != nil {
			return err
		}
		rows[i].Fees = fees
	}
	return nil
}

// chainFees sums a settlement chain's fees across its rounds, or returns nil
// while any round has no fees yet. Every round's fee and delivery decimals
// must agree (TR-47).
func chainFees(rounds []*types.CrossMeshSettlement) (*DepositFees, error) {
	decimals := -1
	agrees := func(d int) bool {
		if decimals == -1 {
			decimals = d
		}
		return d == decimals
	}
	for _, st := range rounds {
		if (st.Fees != nil && !agrees(st.Fees.Decimals)) || (st.Delivery != nil && !agrees(st.Delivery.Decimals)) {
			return nil, fmt.Errorf("settlement %s: fee and delivery decimals disagree across rounds", st.SettlementID)
		}
	}

	setup, settlement, cctp, delivered := new(big.Int), new(big.Int), new(big.Int), new(big.Int)
	cctpKnown, deliveredKnown := true, true
	for _, st := range rounds {
		if st.Fees == nil {
			return nil, nil
		}
		if err := addAmount(setup, st.Fees.SetupFee); err != nil {
			return nil, err
		}
		if err := addAmount(settlement, st.Fees.SettlementFee); err != nil {
			return nil, err
		}
		if st.Fees.CCTPFee == nil {
			cctpKnown = false
		} else if err := addAmount(cctp, *st.Fees.CCTPFee); err != nil {
			return nil, err
		}
		if st.Delivery == nil {
			deliveredKnown = false
		} else if err := addAmount(delivered, st.Delivery.Amount); err != nil {
			return nil, err
		}
	}

	fees := &DepositFees{SetupFee: setup.String(), SettlementFee: settlement.String(), Decimals: decimals}
	if cctpKnown {
		cctpFee := cctp.String()
		total := new(big.Int).Add(setup, settlement)
		totalFee := total.Add(total, cctp).String()
		fees.CCTPFee, fees.TotalFee = &cctpFee, &totalFee
	}
	if deliveredKnown {
		amount := delivered.String()
		fees.DeliveredAmount = &amount
	}
	return fees, nil
}

// addAmount adds the base-unit integer string amount to sum.
func addAmount(sum *big.Int, amount string) error {
	n, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return fmt.Errorf("decoding amount %q", amount)
	}
	sum.Add(sum, n)
	return nil
}

func isBelowMinimum(r DepositRow) bool {
	return r.Status == "held" && r.HoldReason != nil && *r.HoldReason == "below_minimum"
}

// addRemainingToMinimum sets remaining_to_minimum on held below_minimum rows
// (TR-49): the chain's min_settlement_amount minus every held deposit on that
// chain, or null when nothing remains. Chains are fetched only when such a
// row exists.
func (s *CrossChainDepositService) addRemainingToMinimum(ctx context.Context, network string, rows []DepositRow) error {
	held := map[string]*big.Int{}
	needed := false
	for _, r := range rows {
		if r.Status != "held" {
			continue
		}
		if held[r.SourceChain] == nil {
			held[r.SourceChain] = new(big.Int)
		}
		if err := addAmount(held[r.SourceChain], r.Amount); err != nil {
			return err
		}
		needed = needed || isBelowMinimum(r)
	}
	if !needed {
		return nil
	}

	chains, err := s.GetChains(ctx, network)
	if err != nil {
		return err
	}
	for i := range rows {
		if !isBelowMinimum(rows[i]) {
			continue
		}
		for _, c := range chains.Chains {
			if c.Chain != rows[i].SourceChain {
				continue
			}
			if c.Decimals != rows[i].Decimals {
				return fmt.Errorf("chain %s decimals %d disagree with deposit %s decimals %d", c.Chain, c.Decimals, rows[i].DepositID, rows[i].Decimals)
			}
			remaining := new(big.Int)
			if err = addAmount(remaining, c.MinSettlementAmount); err != nil {
				return err
			}
			if remaining.Sub(remaining, held[c.Chain]).Sign() > 0 {
				rows[i].RemainingToMinimum = &DepositAmount{Amount: remaining.String(), Decimals: c.Decimals}
			}
		}
	}
	return nil
}
