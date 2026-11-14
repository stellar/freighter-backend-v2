// ABOUTME: Checks that a Stellar account exists and trusts Circle-issued USDC before a deposit address is shown.
// ABOUTME: Reads the account and its USDC trustline in one getLedgerEntries call through the RPC service.
package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

var (
	// ErrAccountNotFound means the account does not exist on the ledger.
	ErrAccountNotFound = errors.New("account not found")
	// ErrTrustlineMissing means the account has no USDC trustline, or one
	// with a zero limit, so it cannot receive USDC.
	ErrTrustlineMissing = errors.New("usdc trustline missing")
)

// CheckUSDCTrustline returns nil when account exists and holds a USDC
// trustline (to issuer) with a non-zero limit, ErrAccountNotFound or
// ErrTrustlineMissing otherwise, and a wrapped error when the RPC call fails.
// account must be a G address.
func CheckUSDCTrustline(ctx context.Context, rpc types.RPCService, network, account, issuer string) error {
	accountID, err := xdr.AddressToAccountId(account)
	if err != nil {
		return fmt.Errorf("invalid account: %w", err)
	}
	usdc, err := xdr.NewCreditAsset("USDC", issuer)
	if err != nil {
		return fmt.Errorf("invalid USDC issuer: %w", err)
	}

	var accountKey, trustlineKey xdr.LedgerKey
	if err = accountKey.SetAccount(accountID); err != nil {
		return err
	}
	if err = trustlineKey.SetTrustline(accountID, usdc.ToTrustLineAsset()); err != nil {
		return err
	}
	accountKeyB64, err := xdr.MarshalBase64(accountKey)
	if err != nil {
		return err
	}
	trustlineKeyB64, err := xdr.MarshalBase64(trustlineKey)
	if err != nil {
		return err
	}

	entries, err := rpc.GetLedgerEntriesXDR(ctx, []string{accountKeyB64, trustlineKeyB64}, network)
	if err != nil {
		return fmt.Errorf("checking USDC trustline: %w", err)
	}
	if _, ok := entries[accountKeyB64]; !ok {
		return ErrAccountNotFound
	}
	trustlineB64, ok := entries[trustlineKeyB64]
	if !ok {
		return ErrTrustlineMissing
	}
	var data xdr.LedgerEntryData
	if err = xdr.SafeUnmarshalBase64(trustlineB64, &data); err != nil {
		return fmt.Errorf("decoding USDC trustline: %w", err)
	}
	if line, isLine := data.GetTrustLine(); !isLine || line.Limit == 0 {
		return ErrTrustlineMissing
	}
	return nil
}
