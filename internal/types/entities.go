package types

import "github.com/stellar/go/xdr"

type GetHealthResponse struct {
	Status string `json:"status"`
}

type SimulateTransactionResponse = *xdr.ScVal

type LedgerEntryMap struct {
	Account AccountInfo
	
}

type Signer struct {
	Key string `json:"key"`
	Weight uint64 `json:"weight"`
}

type AccountInfo struct {
	AccountId string `json:"account_id"`
	Balance string `json:"balance"`
	Seq_num string `json:"seq_num"`
	Num_sub_entries uint64 `json:"num_sub_entries"`
	Inflation_dest string `json:"inflation_dest"`
	Flags uint64 `json:"flags"`
	HomeDomain string `json:"home_domain"`
	Thresholds string `json:"thresholds"`
	Signers []Signer `json:"signers"`
	Ext xdr.LedgerEntryExt `json:"ext"`
	SequenceNumber uint64 `json:"sequence_number"`
}