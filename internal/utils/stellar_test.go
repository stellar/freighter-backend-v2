package utils

import (
	"testing"

	"github.com/stellar/go/strkey"
	"github.com/stretchr/testify/assert"
)

func TestIsValidContractID(t *testing.T) {
	// Generate a valid contract ID string using strkey
	validRaw := make([]byte, 32)
	for i := 0; i < 32; i++ {
		validRaw[i] = byte(i)
	}
	validContractID, err := strkey.Encode(strkey.VersionByteContract, validRaw)
	assert.NoError(t, err)

	t.Run("valid contract ID", func(t *testing.T) {
		assert.True(t, IsValidContractID(validContractID))
	})

	t.Run("invalid prefix", func(t *testing.T) {
		// A valid G-address (account ID) should fail
		accountID, err := strkey.Encode(strkey.VersionByteAccountID, validRaw)
		assert.NoError(t, err)
		assert.False(t, IsValidContractID(accountID))
	})

	t.Run("wrong length", func(t *testing.T) {
		shortRaw := make([]byte, 16)
		shortContractID, err := strkey.Encode(strkey.VersionByteContract, shortRaw)
		assert.NoError(t, err)
		assert.False(t, IsValidContractID(shortContractID))
	})

	t.Run("malformed string", func(t *testing.T) {
		assert.False(t, IsValidContractID("this-is-not-a-contract-id"))
	})
}

func TestIsValidStellarPublicKey(t *testing.T) {
	// Generate a valid public key (account ID)
	validRaw := make([]byte, 32)
	for i := range validRaw {
		validRaw[i] = byte(255 - i) // just a dummy value
	}
	validPubKey, err := strkey.Encode(strkey.VersionByteAccountID, validRaw)
	assert.NoError(t, err)

	t.Run("valid public key", func(t *testing.T) {
		assert.True(t, IsValidStellarPublicKey(validPubKey))
	})

	t.Run("wrong version byte", func(t *testing.T) {
		contractID, err := strkey.Encode(strkey.VersionByteContract, validRaw)
		assert.NoError(t, err)
		assert.False(t, IsValidStellarPublicKey(contractID))
	})

	t.Run("wrong length", func(t *testing.T) {
		shortRaw := make([]byte, 16)
		shortKey, err := strkey.Encode(strkey.VersionByteAccountID, shortRaw)
		assert.NoError(t, err)
		assert.False(t, IsValidStellarPublicKey(shortKey))
	})

	t.Run("malformed string", func(t *testing.T) {
		assert.False(t, IsValidStellarPublicKey("not-a-public-key"))
	})
}
