// ABOUTME: Tests for the cross-chain deposits route, run against the real service with a mocked Cross Mesh.
// ABOUTME: Covers ordering, user_state, fee attribution across rounds, remaining_to_minimum, errors and the settlement cache.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

const ccDepositsTarget = "/accounts/" + ccAccount + "/deposits?network=TESTNET"

// ccDeposit is a 1 USDC deposit on polygon-amoy, created and updated at at.
func ccDeposit(id, status, at string) types.CrossMeshDeposit {
	return types.CrossMeshDeposit{
		DepositID: id, Status: status, Amount: "1000000", Decimals: 6,
		SourceChain: "polygon-amoy", SourceChainID: 80002, SourceTx: "0xsrc_" + id,
		CreatedAt: at, UpdatedAt: at,
	}
}

// ccSettled is a completed deposit carried by settlement stl.
func ccSettled(id, at, stl string) types.CrossMeshDeposit {
	d := ccDeposit(id, "completed", at)
	d.SettlementID = strPtr(stl)
	return d
}

// ccHeld is a held deposit with the given hold_reason and amount on chain.
func ccHeld(id, reason, amount, chain string) types.CrossMeshDeposit {
	d := ccDeposit(id, "held", "2026-11-01T10:00:00Z")
	d.HoldReason, d.Amount, d.SourceChain = strPtr(reason), amount, chain
	return d
}

// ccRound is a completed settlement round with fees; a nil cctp or delivered
// leaves cctp_fee or delivery null.
func ccRound(id string, prev *string, setup, fee string, cctp, delivered *string) *types.CrossMeshSettlement {
	st := &types.CrossMeshSettlement{
		SettlementID: id, Status: "completed", ContinuationOf: prev,
		Fees: &types.CrossMeshSettlementFees{SetupFee: setup, SettlementFee: fee, CCTPFee: cctp, Decimals: 6},
	}
	if delivered != nil {
		st.Delivery = &types.CrossMeshDelivery{Asset: "USDC", Amount: *delivered, Decimals: 6}
	}
	return st
}

func (f *ccFixture) onePage(deposits ...types.CrossMeshDeposit) {
	f.pages = map[string]*types.CrossMeshDepositPage{"": {Data: deposits}}
}

// depositRows calls the deposits route, requires a 200, and returns the rows
// in response order.
func (f *ccFixture) depositRows(t *testing.T) []map[string]any {
	t.Helper()
	rec := f.serve(http.MethodGet, ccDepositsTarget, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data struct {
			Deposits []map[string]any `json:"deposits"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Data.Deposits
}

func ccRowsByID(rows []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[r["deposit_id"].(string)] = r
	}
	return out
}

// ccFees is the expected fees object; a nil cctp, total or delivered is null.
func ccFees(setup, fee string, cctp, total, delivered *string) map[string]any {
	orNil := func(s *string) any {
		if s == nil {
			return nil
		}
		return *s
	}
	return map[string]any{
		"setup_fee": setup, "settlement_fee": fee, "cctp_fee": orNil(cctp),
		"total_fee": orNil(total), "delivered_amount": orNil(delivered), "decimals": float64(6),
	}
}

func ccRowsWithFees(rows []map[string]any) int {
	n := 0
	for _, r := range rows {
		if r["fees"] != nil {
			n++
		}
	}
	return n
}

func TestDeposits_TR15_RejectsNonGAccounts(t *testing.T) {
	muxed, err := strkey.Encode(strkey.VersionByteMuxedAccount, make([]byte, 40))
	require.NoError(t, err)
	contract, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	require.NoError(t, err)

	for name, account := range map[string]string{"muxed": muxed, "contract": contract, "garbage": "not-an-account"} {
		t.Run(name, func(t *testing.T) {
			f := newCCFixture(t)
			rec := f.serve(http.MethodGet, "/accounts/"+account+"/deposits?network=TESTNET", "")
			requireCCError(t, rec, http.StatusBadRequest, "invalid_account")
			assert.Empty(t, f.listCursors)
		})
	}
}

func TestDeposits_TR35_NewestFirstWithTiebreak(t *testing.T) {
	f := newCCFixture(t)
	f.pages = map[string]*types.CrossMeshDepositPage{
		"": {
			Data:    []types.CrossMeshDeposit{ccDeposit("dep_a", "confirmed", "2026-11-01T10:00:00Z"), ccDeposit("dep_b", "confirmed", "2026-11-01T12:00:00Z")},
			HasMore: true, NextCursor: strPtr("cur_1"),
		},
		"cur_1": {Data: []types.CrossMeshDeposit{
			ccDeposit("dep_c", "confirmed", "2026-11-01T11:00:00Z"),
			ccDeposit("dep_d", "confirmed", "2026-11-01T12:00:00Z"),
			// Half a second later than dep_b and dep_d, though it sorts
			// before them as a string.
			ccDeposit("dep_e", "confirmed", "2026-11-01T12:00:00.5Z"),
		}},
	}

	var ids []string
	for _, r := range f.depositRows(t) {
		ids = append(ids, r["deposit_id"].(string))
	}
	assert.Equal(t, []string{"dep_e", "dep_d", "dep_b", "dep_c", "dep_a"}, ids)
	assert.Equal(t, []string{"", "cur_1"}, f.listCursors, "every page is followed to the end")
	assert.Equal(t, []int{100, 100}, f.listLimits)
}

func TestDeposits_TR36_37_UserStates(t *testing.T) {
	cases := []struct {
		status string
		reason *string
		want   string
	}{
		{"confirmed", nil, "pending"},
		{"settling", nil, "pending"},
		{"completed", nil, "completed"},
		{"held", strPtr("below_minimum"), "action_needed"},
		{"held", strPtr("recipient_not_ready"), "action_needed"},
		{"held", strPtr("external_settlement_unverified"), "under_review"},
		{"held", strPtr("chain_unavailable"), "pending"},
		{"held", strPtr("gas_too_high"), "pending"},
		{"held", strPtr("operator_not_allowed"), "pending"},
		{"held", strPtr("reorg_check"), "pending"},
		{"held", strPtr("conversion_unavailable"), "pending"},
		{"held", strPtr("a_reason_added_later"), "pending"},
		{"held", nil, "pending"},
		{"a_status_added_later", nil, "pending"},
	}
	for _, tc := range cases {
		name := tc.status
		if tc.reason != nil {
			name += "/" + *tc.reason
		}
		t.Run(name, func(t *testing.T) {
			f := newCCFixture(t)
			d := ccDeposit("dep_1", tc.status, "2026-11-01T10:00:00Z")
			d.HoldReason = tc.reason
			f.onePage(d)
			rows := f.depositRows(t)
			require.Len(t, rows, 1)
			assert.Equal(t, tc.want, rows[0]["user_state"])
		})
	}
}

func TestDeposits_TR38_40_41_51_RowFields(t *testing.T) {
	f := newCCFixture(t)
	done := ccSettled("dep_done", "2026-11-01T10:00:00Z", "stl_1")
	done.UpdatedAt = "2026-11-01T10:30:00Z"
	held := ccHeld("dep_held", "recipient_not_ready", "2500000", "ethereum-sepolia")
	held.CreatedAt, held.UpdatedAt = "2026-11-01T09:00:00Z", "2026-11-01T09:05:00Z"
	held.SourceChainID = 11155111
	held.HoldDetail = strPtr("Recipient has no USDC trustline")
	f.onePage(held, done)
	// Completed with amounts unknown: no fees, no delivery, no destination_tx.
	f.settlements = map[string]*types.CrossMeshSettlement{"stl_1": {SettlementID: "stl_1", Status: "completed"}}

	rec := f.serve(http.MethodGet, ccDepositsTarget, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"data":{"deposits":[
		{"deposit_id":"dep_done","status":"completed","hold_reason":null,"hold_detail":null,"user_state":"completed",
		 "amount":"1000000","decimals":6,"source_chain":"polygon-amoy","source_chain_id":80002,"source_tx":"0xsrc_dep_done",
		 "destination_tx":null,"settlement_id":"stl_1","created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-01T10:30:00Z",
		 "remaining_to_minimum":null,"fees":null},
		{"deposit_id":"dep_held","status":"held","hold_reason":"recipient_not_ready","hold_detail":"Recipient has no USDC trustline",
		 "user_state":"action_needed","amount":"2500000","decimals":6,"source_chain":"ethereum-sepolia","source_chain_id":11155111,
		 "source_tx":"0xsrc_dep_held","destination_tx":null,"settlement_id":null,"created_at":"2026-11-01T09:00:00Z",
		 "updated_at":"2026-11-01T09:05:00Z","remaining_to_minimum":null,"fees":null}
	]}}`, rec.Body.String())
}

func TestDeposits_TR39_SingleRoundFeesOnNewestRow(t *testing.T) {
	f := newCCFixture(t)
	f.onePage(
		ccSettled("dep_1", "2026-11-01T10:00:00Z", "stl_1"),
		ccSettled("dep_2", "2026-11-01T11:00:00Z", "stl_1"),
		ccDeposit("dep_3", "confirmed", "2026-11-01T12:00:00Z"),
	)
	f.settlements = map[string]*types.CrossMeshSettlement{
		"stl_1": ccRound("stl_1", nil, "100000", "50000", strPtr("0"), strPtr("1850000")),
	}

	rows := ccRowsByID(f.depositRows(t))
	assert.Equal(t, ccFees("100000", "50000", strPtr("0"), strPtr("150000"), strPtr("1850000")), rows["dep_2"]["fees"])
	assert.Nil(t, rows["dep_1"]["fees"])
	assert.Nil(t, rows["dep_3"]["fees"])
	assert.Equal(t, 1, f.settlementCalls["stl_1"], "a shared settlement is fetched once")
}

func TestDeposits_TR39_TwoRoundsWithUnreferencedFirstRound(t *testing.T) {
	t.Run("rounds are summed onto the newest deposit", func(t *testing.T) {
		f := newCCFixture(t)
		// stl_1 ← stl_2: every deposit moved to the second round, so no
		// deposit references stl_1.
		f.onePage(
			ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_2"),
			ccSettled("dep_b", "2026-11-01T11:00:00Z", "stl_2"),
		)
		f.settlements = map[string]*types.CrossMeshSettlement{
			"stl_1": ccRound("stl_1", nil, "100000", "50000", strPtr("0"), strPtr("10000000")),
			"stl_2": ccRound("stl_2", strPtr("stl_1"), "0", "50000", strPtr("1000"), strPtr("2000000")),
		}

		rows := f.depositRows(t)
		byID := ccRowsByID(rows)
		assert.Equal(t, ccFees("100000", "100000", strPtr("1000"), strPtr("201000"), strPtr("12000000")), byID["dep_b"]["fees"])
		assert.Equal(t, 1, ccRowsWithFees(rows), "fees on exactly one row")
		assert.Equal(t, map[string]int{"stl_1": 1, "stl_2": 1}, f.settlementCalls)
	})

	t.Run("the newest deposit owns the chain whichever round it is in", func(t *testing.T) {
		f := newCCFixture(t)
		f.onePage(
			ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_2"),
			ccSettled("dep_b", "2026-11-01T11:00:00Z", "stl_1"),
		)
		f.settlements = map[string]*types.CrossMeshSettlement{
			"stl_1": ccRound("stl_1", nil, "100000", "50000", strPtr("0"), strPtr("10000000")),
			"stl_2": ccRound("stl_2", strPtr("stl_1"), "0", "50000", strPtr("0"), strPtr("2000000")),
		}

		rows := f.depositRows(t)
		assert.Equal(t, ccFees("100000", "100000", strPtr("0"), strPtr("200000"), strPtr("12000000")), ccRowsByID(rows)["dep_b"]["fees"])
		assert.Equal(t, 1, ccRowsWithFees(rows))
	})

	t.Run("a continuation_of cycle terminates", func(t *testing.T) {
		f := newCCFixture(t)
		f.onePage(ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_x"))
		f.settlements = map[string]*types.CrossMeshSettlement{
			"stl_x": ccRound("stl_x", strPtr("stl_y"), "0", "50000", strPtr("0"), strPtr("1000000")),
			"stl_y": ccRound("stl_y", strPtr("stl_x"), "0", "50000", strPtr("0"), strPtr("1000000")),
		}

		rows := f.depositRows(t)
		assert.Equal(t, ccFees("0", "100000", strPtr("0"), strPtr("100000"), strPtr("2000000")), rows[0]["fees"])
		assert.Equal(t, map[string]int{"stl_x": 1, "stl_y": 1}, f.settlementCalls)
	})
}

func TestDeposits_TR39_NullFeesRoundHidesFees(t *testing.T) {
	f := newCCFixture(t)
	settling := ccDeposit("dep_b", "settling", "2026-11-01T11:00:00Z")
	settling.SettlementID = strPtr("stl_2")
	f.onePage(ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_1"), settling)
	f.settlements = map[string]*types.CrossMeshSettlement{
		"stl_1": ccRound("stl_1", nil, "100000", "50000", strPtr("0"), strPtr("10000000")),
		"stl_2": {SettlementID: "stl_2", Status: "settling", ContinuationOf: strPtr("stl_1")},
	}

	assert.Zero(t, ccRowsWithFees(f.depositRows(t)), "fees appear only once every round has them")
}

func TestDeposits_TR39_NullCctpFeeAndNullDelivery(t *testing.T) {
	// Two rounds; the second is still missing cctp_fee or delivery.
	cases := map[string]struct {
		cctp, delivered *string
		want            map[string]any
	}{
		"null cctp_fee": {nil, strPtr("2000000"), ccFees("100000", "100000", nil, nil, strPtr("12000000"))},
		"null delivery": {strPtr("0"), nil, ccFees("100000", "100000", strPtr("0"), strPtr("200000"), nil)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCCFixture(t)
			f.onePage(ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_2"))
			f.settlements = map[string]*types.CrossMeshSettlement{
				"stl_1": ccRound("stl_1", nil, "100000", "50000", strPtr("0"), strPtr("10000000")),
				"stl_2": ccRound("stl_2", strPtr("stl_1"), "0", "50000", tc.cctp, tc.delivered),
			}
			assert.Equal(t, tc.want, f.depositRows(t)[0]["fees"])
		})
	}
}

func TestDeposits_TR47_DecimalsMismatchReturns502(t *testing.T) {
	captureLogs(t)
	cases := map[string]func(f *ccFixture){
		"fee and delivery decimals in one round": func(f *ccFixture) {
			f.onePage(ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_1"))
			st := ccRound("stl_1", nil, "100000", "50000", strPtr("0"), strPtr("1850000"))
			st.Delivery.Decimals = 7
			f.settlements = map[string]*types.CrossMeshSettlement{"stl_1": st}
		},
		"fee decimals across rounds": func(f *ccFixture) {
			f.onePage(ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_2"))
			second := ccRound("stl_2", strPtr("stl_1"), "0", "50000", nil, nil)
			second.Fees.Decimals = 7
			f.settlements = map[string]*types.CrossMeshSettlement{
				"stl_1": ccRound("stl_1", nil, "100000", "50000", nil, nil),
				"stl_2": second,
			}
		},
		"chain and deposit decimals": func(f *ccFixture) {
			f.onePage(ccHeld("dep_a", "below_minimum", "300000", "polygon-amoy"))
			f.chains.Chains[0].Decimals = 7
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCCFixture(t)
			setup(f)
			requireCCError(t, f.serve(http.MethodGet, ccDepositsTarget, ""), http.StatusBadGateway, "upstream_error")
		})
	}
}

func TestDeposits_TR49_RemainingToMinimum(t *testing.T) {
	t.Run("min minus every held deposit on the chain, null at or below zero", func(t *testing.T) {
		f := newCCFixture(t)
		// polygon-amoy min 1000000; ethereum-sepolia min 5000000.
		completed := ccDeposit("dep_3", "completed", "2026-11-01T08:00:00Z")
		completed.Amount = "900000"
		f.onePage(
			ccHeld("dep_1", "below_minimum", "300000", "polygon-amoy"),
			ccHeld("dep_2", "recipient_not_ready", "200000", "polygon-amoy"),
			completed,
			ccHeld("dep_4", "below_minimum", "5000000", "ethereum-sepolia"),
		)

		rows := ccRowsByID(f.depositRows(t))
		assert.Equal(t, map[string]any{"amount": "500000", "decimals": float64(6)}, rows["dep_1"]["remaining_to_minimum"])
		assert.Nil(t, rows["dep_2"]["remaining_to_minimum"], "only below_minimum rows carry it")
		assert.Nil(t, rows["dep_3"]["remaining_to_minimum"])
		assert.Nil(t, rows["dep_4"]["remaining_to_minimum"], "zero remaining is null")
		assert.Equal(t, 1, f.chainsCalls)
	})

	t.Run("chains are not fetched without a below_minimum row", func(t *testing.T) {
		f := newCCFixture(t)
		f.onePage(ccHeld("dep_1", "recipient_not_ready", "200000", "polygon-amoy"))
		f.depositRows(t)
		assert.Zero(t, f.chainsCalls)
	})

	t.Run("a chains failure is an error", func(t *testing.T) {
		captureLogs(t)
		f := newCCFixture(t)
		f.onePage(ccHeld("dep_1", "below_minimum", "300000", "polygon-amoy"))
		f.chainsErr = ccUpstreamError(http.StatusServiceUnavailable, "unavailable")
		requireCCError(t, f.serve(http.MethodGet, ccDepositsTarget, ""), http.StatusBadGateway, "upstream_error")
	})
}

func TestDeposits_TR52_SettlementFailureReturnsError(t *testing.T) {
	captureLogs(t)
	newFixture := func(t *testing.T) *ccFixture {
		f := newCCFixture(t)
		f.onePage(ccSettled("dep_a", "2026-11-01T10:00:00Z", "stl_1"))
		f.settlements = map[string]*types.CrossMeshSettlement{
			"stl_1": ccRound("stl_1", nil, "100000", "50000", strPtr("0"), strPtr("1850000")),
		}
		return f
	}

	t.Run("upstream 5xx", func(t *testing.T) {
		f := newFixture(t)
		f.settlementErr = func(string, int) error { return ccUpstreamError(http.StatusInternalServerError, "internal_error") }
		requireCCError(t, f.serve(http.MethodGet, ccDepositsTarget, ""), http.StatusServiceUnavailable, "upstream_unavailable")
	})

	t.Run("settlement_not_found once re-lists and succeeds", func(t *testing.T) {
		f := newFixture(t)
		f.settlementErr = func(_ string, call int) error {
			if call == 1 {
				return ccUpstreamError(http.StatusNotFound, "settlement_not_found")
			}
			return nil
		}
		rows := f.depositRows(t)
		assert.NotNil(t, rows[0]["fees"])
		assert.Equal(t, []string{"", ""}, f.listCursors, "deposits are listed again once")
	})

	t.Run("settlement_not_found twice returns 502", func(t *testing.T) {
		f := newFixture(t)
		delete(f.settlements, "stl_1")
		extras := requireCCError(t, f.serve(http.MethodGet, ccDepositsTarget, ""), http.StatusBadGateway, "upstream_error")
		assert.Equal(t, "settlement_not_found", extras["upstream_code"])
		assert.Len(t, f.listCursors, 2)
	})

	t.Run("timeout returns 504", func(t *testing.T) {
		f := newFixture(t)
		f.settlementErr = func(string, int) error { return fmt.Errorf("crossmesh GetSettlement: %w", context.DeadlineExceeded) }
		requireCCError(t, f.serve(http.MethodGet, ccDepositsTarget, ""), http.StatusGatewayTimeout, "upstream_timeout")
	})
}

func TestDeposits_D9_CompletedSettlementCached(t *testing.T) {
	f := newCCFixture(t)
	cache := &ccMapCache{entries: map[string][]byte{}}
	f.cache = cache
	f.onePage(
		ccSettled("dep_done", "2026-11-01T10:00:00Z", "stl_done"),
		ccSettled("dep_attesting", "2026-11-01T11:00:00Z", "stl_attesting"),
		ccSettled("dep_delivering", "2026-11-01T12:00:00Z", "stl_delivering"),
	)
	f.settlements = map[string]*types.CrossMeshSettlement{
		"stl_done":       ccRound("stl_done", nil, "100000", "50000", strPtr("0"), strPtr("850000")),
		"stl_attesting":  ccRound("stl_attesting", nil, "0", "50000", nil, nil),
		"stl_delivering": ccRound("stl_delivering", nil, "0", "50000", strPtr("0"), nil),
	}

	first := f.serve(http.MethodGet, ccDepositsTarget, "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := f.serve(http.MethodGet, ccDepositsTarget, "")
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())

	assert.JSONEq(t, first.Body.String(), second.Body.String())
	assert.Equal(t, map[string]int{"stl_done": 1, "stl_attesting": 2, "stl_delivering": 2}, f.settlementCalls)
	assert.Equal(t, services.CrossMeshSettlementCacheTTL, cache.ttls["crossmesh:settlement:v1:TESTNET:stl_done"])
	assert.Len(t, cache.entries, 1, "only the completed settlement is cached")
}
