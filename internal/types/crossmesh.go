// ABOUTME: Wire types for the Cross Mesh Ingress API (chains, deposit addresses, deposits, settlements).
// ABOUTME: Field names and nullability follow the testnet OpenAPI spec; nullable fields are pointers.
package types

// CrossMeshChains is the GET /v1/chains response. The contracts and
// xlm_delivery blocks are not decoded: contracts are pinned in code and XLM
// delivery is not offered.
type CrossMeshChains struct {
	Chains []CrossMeshChain `json:"chains"`
}

// CrossMeshChain is one source chain of an environment.
type CrossMeshChain struct {
	Chain               string              `json:"chain"`
	ChainID             int64               `json:"chain_id"`
	Status              string              `json:"status"`
	USDCAddress         string              `json:"usdc_address"`
	Confirmations       int64               `json:"confirmations"`
	MinSettlementAmount string              `json:"min_settlement_amount"`
	Decimals            int                 `json:"decimals"`
	Speeds              CrossMeshSpeeds     `json:"speeds"`
	Fees                *CrossMeshChainFees `json:"fees"`
}

// CrossMeshSpeeds holds a chain's transfer speeds. Only standard is decoded:
// Freighter creates standard addresses only.
type CrossMeshSpeeds struct {
	Standard CrossMeshSpeed `json:"standard"`
}

// CrossMeshSpeed is one transfer speed on a chain.
type CrossMeshSpeed struct {
	EstimatedDeliverySeconds *int64 `json:"estimated_delivery_seconds"`
}

// CrossMeshChainFees are the fees Cross Mesh charges per settlement on a chain.
type CrossMeshChainFees struct {
	SetupFee   string `json:"setup_fee"`
	BaseFee    string `json:"base_fee"`
	FeeRatePPM string `json:"fee_rate_ppm"`
	Decimals   int    `json:"decimals"`
}

// CrossMeshDepositAddress is a deposit address row, as returned by
// POST /v1/deposit-addresses and in the GET /v1/deposit-addresses list.
type CrossMeshDepositAddress struct {
	DepositAddress string `json:"deposit_address"`
	Recipient      string `json:"recipient"`
	DeliveryAsset  string `json:"delivery_asset"`
	Speed          string `json:"speed"`
	Index          uint64 `json:"index"`
	CreatedAt      string `json:"created_at"`
}

// CrossMeshDepositAddressPage is one page of GET /v1/deposit-addresses.
type CrossMeshDepositAddressPage struct {
	Data       []CrossMeshDepositAddress `json:"data"`
	HasMore    bool                      `json:"has_more"`
	NextCursor *string                   `json:"next_cursor"`
}

// CrossMeshDeposit is one incoming USDC transfer to a deposit address.
type CrossMeshDeposit struct {
	DepositID     string  `json:"deposit_id"`
	Status        string  `json:"status"`
	HoldReason    *string `json:"hold_reason"`
	HoldDetail    *string `json:"hold_detail"`
	SettlementID  *string `json:"settlement_id"`
	Amount        string  `json:"amount"`
	Decimals      int     `json:"decimals"`
	SourceChain   string  `json:"source_chain"`
	SourceChainID int64   `json:"source_chain_id"`
	SourceTx      string  `json:"source_tx"`
	DestinationTx *string `json:"destination_tx"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// CrossMeshDepositPage is one page of GET /v1/deposits.
type CrossMeshDepositPage struct {
	Data       []CrossMeshDeposit `json:"data"`
	HasMore    bool               `json:"has_more"`
	NextCursor *string            `json:"next_cursor"`
}

// CrossMeshSettlement is GET /v1/settlements/{settlement_id}: one settlement
// round carrying one or more deposits.
type CrossMeshSettlement struct {
	SettlementID   string                   `json:"settlement_id"`
	Status         string                   `json:"status"`
	HoldReason     *string                  `json:"hold_reason"`
	Fees           *CrossMeshSettlementFees `json:"fees"`
	Delivery       *CrossMeshDelivery       `json:"delivery"`
	ContinuationOf *string                  `json:"continuation_of"`
}

// CrossMeshSettlementFees are the fees actually charged in a settlement.
type CrossMeshSettlementFees struct {
	SetupFee      string  `json:"setup_fee"`
	SettlementFee string  `json:"settlement_fee"`
	CCTPFee       *string `json:"cctp_fee"`
	Decimals      int     `json:"decimals"`
}

// CrossMeshDelivery is what the recipient received on Stellar.
type CrossMeshDelivery struct {
	Asset    string `json:"asset"`
	Amount   string `json:"amount"`
	Decimals int    `json:"decimals"`
}
