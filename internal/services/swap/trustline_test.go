package swap

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func classicTx(t *testing.T, withTrust bool) (string, string) {
	t.Helper()
	kp := keypair.MustRandom()
	usdc := txnbuild.CreditAsset{Code: "USDC", Issuer: usdcIssuer}
	ops := []txnbuild.Operation{}
	if withTrust {
		line, _ := usdc.ToChangeTrustAsset()
		ops = append(ops, &txnbuild.ChangeTrust{Line: line, Limit: txnbuild.MaxTrustlineLimit})
	}
	ops = append(ops, &txnbuild.PathPaymentStrictSend{SendAsset: txnbuild.NativeAsset{}, SendAmount: "1",
		Destination: kp.Address(), DestAsset: usdc, DestMin: "0.1"})
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount: &txnbuild.SimpleAccount{AccountID: kp.Address(), Sequence: 1},
		Operations:    ops, BaseFee: 100, Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(300)},
	})
	require.NoError(t, err)
	b64, err := tx.Base64()
	require.NoError(t, err)
	return b64, kp.Address()
}

func TestAddsTrustlineClassic(t *testing.T) {
	t.Parallel()
	x, acct := classicTx(t, true)
	got, err := AddsTrustline(x, acct, "")
	require.NoError(t, err)
	assert.True(t, got)

	x, acct = classicTx(t, false)
	got, err = AddsTrustline(x, acct, "")
	require.NoError(t, err)
	assert.False(t, got)
}

func TestAddsTrustlineLifi(t *testing.T) {
	t.Parallel()
	x := trustFixtureXDR(t)
	usdc := contractIDOf(t, "USDC:"+usdcIssuer)
	got, err := AddsTrustline(x, lifiTestAccount, usdc)
	require.NoError(t, err)
	assert.True(t, got)

	got, err = AddsTrustline(x, lifiTestAccount, otherContract)
	require.NoError(t, err)
	assert.False(t, got)
}
