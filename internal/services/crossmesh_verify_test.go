// ABOUTME: Tests for the local CREATE2 derivation of Cross Mesh deposit addresses.
// ABOUTME: Pins Cross Mesh's published testnet vectors and covers mismatches and EIP-55 output (TR-21, TR-24, TR-28).
package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Published by Cross Mesh in the testnet OpenAPI spec ("Verifying an address").
const (
	vectorRecipient      = "GBRAD6AQVETCVZDBT3TLU3KXGPQIXOIQX5DLJYKHNZ4ZF6AWYQXEZGQD"
	vectorFactory        = "0xAD93f57DeA4F6bE812c0e1790dF5D3d1161BEd09"
	vectorImplementation = "0xb0216D95AFf228EB8b3A53C58f780D115eFfEC0F"
	vectorStandard       = "0x93845A25A3a5F9b15c25532797CE56fD4851Fcce"
	vectorFast           = "0x36b10Bac9dfb4C10947dCd49cB480594F0a1124B"
)

func TestComputeDepositAddress_TR21_StandardVector(t *testing.T) {
	t.Parallel()
	got, err := ComputeDepositAddress(vectorRecipient, 0, false, vectorFactory, vectorImplementation)
	require.NoError(t, err)
	assert.Equal(t, vectorStandard, got)
}

func TestComputeDepositAddress_TR21_FastVector(t *testing.T) {
	t.Parallel()
	got, err := ComputeDepositAddress(vectorRecipient, 0, true, vectorFactory, vectorImplementation)
	require.NoError(t, err)
	assert.Equal(t, vectorFast, got)
}

func TestComputeDepositAddress_TR21_RejectsMalformedContract(t *testing.T) {
	t.Parallel()
	_, err := ComputeDepositAddress(vectorRecipient, 0, false, "0x1234", vectorImplementation)
	require.Error(t, err)
	_, err = ComputeDepositAddress(vectorRecipient, 0, false, vectorFactory, "not-hex")
	require.Error(t, err)
}

func TestChecksumAddress_TR28_EIP55(t *testing.T) {
	t.Parallel()
	// Test vectors from EIP-55 itself.
	for _, want := range []string{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
	} {
		raw := mustDecodeHexAddress(t, strings.ToLower(want))
		assert.Equal(t, want, checksumAddress(raw))
	}
}

func TestVerifyDepositAddress_TR24_MismatchFails(t *testing.T) {
	t.Parallel()
	otherImplementation := "0x0000000000000000000000000000000000000001"
	otherRecipient := "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

	cases := map[string]struct {
		recipient      string
		index          uint64
		implementation string
	}{
		"wrong index":          {vectorRecipient, 1, vectorImplementation},
		"wrong implementation": {vectorRecipient, 0, otherImplementation},
		"wrong recipient":      {otherRecipient, 0, vectorImplementation},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			derived, ok := VerifyDepositAddress(vectorStandard, tc.recipient, tc.index, vectorFactory, tc.implementation)
			assert.False(t, ok)
			assert.NotEqual(t, vectorStandard, derived)
		})
	}

	t.Run("fast address does not verify as standard", func(t *testing.T) {
		t.Parallel()
		_, ok := VerifyDepositAddress(vectorFast, vectorRecipient, 0, vectorFactory, vectorImplementation)
		assert.False(t, ok)
	})
}

func TestVerifyDepositAddress_TR28_LowercaseUpstreamReturnsEIP55(t *testing.T) {
	t.Parallel()
	derived, ok := VerifyDepositAddress(strings.ToLower(vectorStandard), vectorRecipient, 0, vectorFactory, vectorImplementation)
	assert.True(t, ok)
	assert.Equal(t, vectorStandard, derived)
}

func mustDecodeHexAddress(t *testing.T, s string) []byte {
	t.Helper()
	b, err := decodeHexAddress(s)
	require.NoError(t, err)
	return b
}
