// ABOUTME: Tests for the USDC trustline check run before a deposit address is shown.
// ABOUTME: Covers TR-16: missing account, missing trustline, zero limit, present trustline, and RPC failure.
package services

import (
	"context"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	eligibilityAccount = "GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD"
	eligibilityIssuer  = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
)

// eligibilityKeys returns the base64 account and USDC trustline ledger keys
// the check must request.
func eligibilityKeys(t *testing.T) (accountKey, trustlineKey string) {
	t.Helper()
	id := xdr.MustAddress(eligibilityAccount)
	var acct, tl xdr.LedgerKey
	require.NoError(t, acct.SetAccount(id))
	require.NoError(t, tl.SetTrustline(id, xdr.MustNewCreditAsset("USDC", eligibilityIssuer).ToTrustLineAsset()))
	accountKey, err := xdr.MarshalBase64(acct)
	require.NoError(t, err)
	trustlineKey, err = xdr.MarshalBase64(tl)
	require.NoError(t, err)
	return accountKey, trustlineKey
}

func accountEntry(t *testing.T) string {
	t.Helper()
	b64, err := xdr.MarshalBase64(xdr.LedgerEntryData{
		Type:    xdr.LedgerEntryTypeAccount,
		Account: &xdr.AccountEntry{AccountId: xdr.MustAddress(eligibilityAccount)},
	})
	require.NoError(t, err)
	return b64
}

func trustlineEntry(t *testing.T, limit xdr.Int64) string {
	t.Helper()
	b64, err := xdr.MarshalBase64(xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeTrustline,
		TrustLine: &xdr.TrustLineEntry{
			AccountId: xdr.MustAddress(eligibilityAccount),
			Asset:     xdr.MustNewCreditAsset("USDC", eligibilityIssuer).ToTrustLineAsset(),
			Limit:     limit,
		},
	})
	require.NoError(t, err)
	return b64
}

func TestCheckUSDCTrustline_TR16(t *testing.T) {
	t.Parallel()
	accountKey, trustlineKey := eligibilityKeys(t)
	rpcErr := errors.New("rpc down")

	cases := map[string]struct {
		entries map[string]string
		rpcErr  error
		want    error
	}{
		"account missing":      {entries: map[string]string{}, want: ErrAccountNotFound},
		"trustline missing":    {entries: map[string]string{accountKey: accountEntry(t)}, want: ErrTrustlineMissing},
		"trustline limit zero": {entries: map[string]string{accountKey: accountEntry(t), trustlineKey: trustlineEntry(t, 0)}, want: ErrTrustlineMissing},
		"trustline present":    {entries: map[string]string{accountKey: accountEntry(t), trustlineKey: trustlineEntry(t, 1)}, want: nil},
		"rpc error is wrapped": {rpcErr: rpcErr, want: rpcErr},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			rpc := &utils.MockRPCService{GetLedgerEntriesXDRFunc: func(keys []string, network string) (map[string]string, error) {
				calls++
				assert.Equal(t, []string{accountKey, trustlineKey}, keys)
				assert.Equal(t, types.TESTNET, network)
				return tc.entries, tc.rpcErr
			}}

			err := CheckUSDCTrustline(context.Background(), rpc, types.TESTNET, eligibilityAccount, eligibilityIssuer)
			assert.Equal(t, 1, calls)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
			if tc.rpcErr != nil {
				assert.NotErrorIs(t, err, ErrAccountNotFound)
				assert.NotErrorIs(t, err, ErrTrustlineMissing)
			}
		})
	}
}
