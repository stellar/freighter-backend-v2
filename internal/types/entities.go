package types

import "github.com/stellar/go/xdr"

type GetHealthResponse struct {
	Status string `json:"status"`
}

type SimulateTransactionResponse = *xdr.ScVal

type LedgerEntryMap struct {
	Account AccountInfo
}

type AccountInfo struct {
	AccountId string `json:"account_id"`
	HomeDomain string `json:"home_domain"`
}