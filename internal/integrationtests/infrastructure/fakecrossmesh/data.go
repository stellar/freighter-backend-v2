// ABOUTME: Canned Cross Mesh data for the fake: chains per environment, deposit histories per recipient, settlement rounds.
// ABOUTME: Rows carry the spec fields the backend reads; recipients here must match the integration suite's constants.
package main

import "encoding/json"

// chains returns the chains list. env "mainnet" switches to mainnet chain
// ids and USDC contracts; scenario "all_suspended" suspends every chain.
func chains(env, scenario string) []map[string]any {
	ethereumID, ethereumUSDC := 11155111, "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
	polygonID, polygonUSDC := 80002, "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	if env == "mainnet" {
		ethereumID, ethereumUSDC = 1, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
		polygonID, polygonUSDC = 137, "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"
	}
	status := "active"
	if scenario == "all_suspended" {
		status = "suspended"
	}
	unavailable := map[string]any{"available": false, "max_cctp_fee_ppm": nil, "estimated_delivery_seconds": nil}
	return []map[string]any{
		{
			"chain": "ethereum", "chain_id": ethereumID, "status": status, "usdc_address": ethereumUSDC,
			"confirmations": 3, "min_settlement_amount": "1000000", "decimals": 6,
			"speeds": map[string]any{
				"standard": map[string]any{"available": true, "max_cctp_fee_ppm": nil, "estimated_delivery_seconds": 1080},
				"fast":     unavailable,
			},
			"fees": map[string]any{"setup_fee": "100000", "base_fee": "50000", "fee_rate_ppm": "0", "decimals": 6},
		},
		{
			// No delivery estimate and no fees yet: both are nullable.
			"chain": "polygon", "chain_id": polygonID, "status": status, "usdc_address": polygonUSDC,
			"confirmations": 3, "min_settlement_amount": "1000000", "decimals": 6,
			"speeds": map[string]any{
				"standard": map[string]any{"available": true, "max_cctp_fee_ppm": nil, "estimated_delivery_seconds": nil},
				"fast":     unavailable,
			},
			"fees": nil,
		},
	}
}

// unavailableSettlementID answers 500 (see getSettlement).
const unavailableSettlementID = "stl_unavailable"

// depositsByRecipient holds each recipient's deposits in the order the fake
// serves them, deliberately not newest first so the backend's ordering shows.
var depositsByRecipient = map[string][]map[string]any{
	// Happy path: one completed two-deposit settlement plus one row per other
	// user_state.
	"GDOAELTTQ52OGFCEGWYE6UZOYWK3PCBKDTJ4WM3ZIOVJUQ7BIBJW5WK2": mustRows(`[
		{"deposit_id":"dep_happy_1","status":"completed","hold_reason":null,"hold_detail":null,"settlement_id":"stl_happy",
		 "amount":"5000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,"source_tx":"0xe1",
		 "destination_tx":"5a1e000000000000000000000000000000000000000000000000000000000001",
		 "created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-02T10:30:00Z"},
		{"deposit_id":"dep_happy_3","status":"held","hold_reason":"recipient_not_ready","hold_detail":"no USDC trustline","settlement_id":null,
		 "amount":"3000000","decimals":6,"source_chain":"polygon","source_chain_id":80002,"source_tx":"0xe3",
		 "destination_tx":null,"created_at":"2026-11-03T10:00:00Z","updated_at":"2026-11-03T10:05:00Z"},
		{"deposit_id":"dep_happy_2","status":"completed","hold_reason":null,"hold_detail":null,"settlement_id":"stl_happy",
		 "amount":"2000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,"source_tx":"0xe2",
		 "destination_tx":"5a1e000000000000000000000000000000000000000000000000000000000001",
		 "created_at":"2026-11-02T10:00:00Z","updated_at":"2026-11-02T10:30:00Z"},
		{"deposit_id":"dep_happy_5","status":"held","hold_reason":"external_settlement_unverified","hold_detail":"under review","settlement_id":null,
		 "amount":"4000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,"source_tx":"0xe5",
		 "destination_tx":null,"created_at":"2026-11-05T10:00:00Z","updated_at":"2026-11-05T10:05:00Z"},
		{"deposit_id":"dep_happy_4","status":"confirmed","hold_reason":null,"hold_detail":null,"settlement_id":null,
		 "amount":"1000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,"source_tx":"0xe4",
		 "destination_tx":null,"created_at":"2026-11-04T10:00:00Z","updated_at":"2026-11-04T10:01:00Z"}
	]`),
	// Two rounds: both deposits sit in stl_round_2, which continues
	// stl_round_1; no deposit references stl_round_1.
	"GC3G7ZKLTOIDT6MILJGH7FFBAPY4GG4WPSXTDCXU4AWNFQSYTGCJRH7M": mustRows(`[
		{"deposit_id":"dep_two_1","status":"completed","hold_reason":null,"hold_detail":null,"settlement_id":"stl_round_2",
		 "amount":"3000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,"source_tx":"0xf1",
		 "destination_tx":"5a1e000000000000000000000000000000000000000000000000000000000002",
		 "created_at":"2026-11-06T09:00:00Z","updated_at":"2026-11-06T10:00:00Z"},
		{"deposit_id":"dep_two_2","status":"completed","hold_reason":null,"hold_detail":null,"settlement_id":"stl_round_2",
		 "amount":"2000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,"source_tx":"0xf2",
		 "destination_tx":"5a1e000000000000000000000000000000000000000000000000000000000002",
		 "created_at":"2026-11-06T09:05:00Z","updated_at":"2026-11-06T10:00:00Z"}
	]`),
	// Its one settlement answers 500.
	"GBVOVVM5PN7FCLLF3ZQIVSIINWF62GHCHM7R5FDZLKSKGQZS4MGKZWLC": mustRows(`[
		{"deposit_id":"dep_unavailable_1","status":"settling","hold_reason":null,"hold_detail":null,"settlement_id":"stl_unavailable",
		 "amount":"2000000","decimals":6,"source_chain":"ethereum","source_chain_id":11155111,"source_tx":"0xd1",
		 "destination_tx":null,"created_at":"2026-11-07T09:00:00Z","updated_at":"2026-11-07T09:01:00Z"}
	]`),
}

// settlements are the canned settlement rounds by id.
var settlements = map[string]json.RawMessage{
	"stl_happy": json.RawMessage(`{"settlement_id":"stl_happy","status":"completed","hold_reason":null,"continuation_of":null,
		"fees":{"setup_fee":"100000","settlement_fee":"50000","cctp_fee":"700","decimals":6},
		"delivery":{"asset":"USDC","amount":"6849300","decimals":6}}`),
	"stl_round_1": json.RawMessage(`{"settlement_id":"stl_round_1","status":"completed","hold_reason":null,"continuation_of":null,
		"fees":{"setup_fee":"100000","settlement_fee":"50000","cctp_fee":"300","decimals":6},
		"delivery":{"asset":"USDC","amount":"2849700","decimals":6}}`),
	"stl_round_2": json.RawMessage(`{"settlement_id":"stl_round_2","status":"completed","hold_reason":null,"continuation_of":"stl_round_1",
		"fees":{"setup_fee":"0","settlement_fee":"50000","cctp_fee":"200","decimals":6},
		"delivery":{"asset":"USDC","amount":"1949800","decimals":6}}`),
}

func mustRows(raw string) []map[string]any {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		panic(err)
	}
	return rows
}
