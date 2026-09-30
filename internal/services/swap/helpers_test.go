package swap

import (
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const testIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

func testContract(b byte) string {
	raw := make([]byte, 32)
	raw[0] = b
	return strkey.MustEncode(strkey.VersionByteContract, raw)
}

// testContractN is a distinct contract id for every i, none equal to testContract.
func testContractN(i int) string {
	raw := make([]byte, 32)
	raw[0], raw[1], raw[2] = 0xFF, byte(i>>8), byte(i)
	return strkey.MustEncode(strkey.VersionByteContract, raw)
}

// mustAddr unwraps a utils address decoder for a fixture value known to be valid.
func mustAddr(a *xdr.ScAddress, err error) xdr.ScAddress {
	if err != nil {
		panic(err)
	}
	return *a
}
