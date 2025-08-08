package types

import "github.com/stellar/go/xdr"

type GetHealthResponse struct {
	Status string `json:"status"`
}

type SimulateTransactionResponse = *xdr.ScVal
