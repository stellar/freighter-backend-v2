package utils

import (
	"testing"

	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
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

func TestIsValidAccount(t *testing.T) {
	// Create valid account ID
	validAccountRaw := make([]byte, 32)
	for i := range validAccountRaw {
		validAccountRaw[i] = byte(i)
	}
	validAccountID, err := strkey.Encode(strkey.VersionByteAccountID, validAccountRaw)
	assert.NoError(t, err)

	// Create valid contract ID
	validContractRaw := make([]byte, 32)
	for i := range validContractRaw {
		validContractRaw[i] = byte(255 - i)
	}
	validContractID, err := strkey.Encode(strkey.VersionByteContract, validContractRaw)
	assert.NoError(t, err)

	t.Run("valid account ID returns true", func(t *testing.T) {
		assert.True(t, IsValidAccount(validAccountID))
	})

	t.Run("valid contract ID returns true", func(t *testing.T) {
		assert.True(t, IsValidAccount(validContractID))
	})

	t.Run("malformed string returns false", func(t *testing.T) {
		assert.False(t, IsValidAccount("not-a-valid-id"))
	})

	t.Run("wrong length returns false", func(t *testing.T) {
		shortRaw := make([]byte, 16)
		shortID, err := strkey.Encode(strkey.VersionByteAccountID, shortRaw)
		assert.NoError(t, err)
		assert.False(t, IsValidAccount(shortID))
	})
}

func TestScAddressFromAccountString(t *testing.T) {
	// Create a valid account ID string
	validRaw := make([]byte, 32)
	for i := range validRaw {
		validRaw[i] = byte(i)
	}
	validAccountID, err := strkey.Encode(strkey.VersionByteAccountID, validRaw)
	assert.NoError(t, err)

	t.Run("valid account ID", func(t *testing.T) {
		scAddr, err := ScAddressFromAccountString(validAccountID)
		assert.NoError(t, err)
		assert.NotNil(t, scAddr)
		assert.Equal(t, xdr.ScAddressTypeScAddressTypeAccount, scAddr.Type)
		assert.NotNil(t, scAddr.AccountId)
		assert.Equal(t, xdr.PublicKeyTypePublicKeyTypeEd25519, scAddr.AccountId.Type)
		assert.Equal(t, validRaw, scAddr.AccountId.Ed25519[:])
	})

	t.Run("malformed account ID", func(t *testing.T) {
		scAddr, err := ScAddressFromAccountString("not-a-valid-key")
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})

	t.Run("wrong length account ID", func(t *testing.T) {
		shortRaw := make([]byte, 16)
		shortAccountID, err := strkey.Encode(strkey.VersionByteAccountID, shortRaw)
		assert.NoError(t, err)

		scAddr, err := ScAddressFromAccountString(shortAccountID)
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})
}

func TestScAddressFromContractString(t *testing.T) {
	// Create a valid contract ID string
	validRaw := make([]byte, 32)
	for i := range validRaw {
		validRaw[i] = byte(255 - i)
	}
	validContractID, err := strkey.Encode(strkey.VersionByteContract, validRaw)
	assert.NoError(t, err)

	t.Run("valid contract ID", func(t *testing.T) {
		scAddr, err := ScAddressFromContractString(validContractID)
		assert.NoError(t, err)
		assert.NotNil(t, scAddr)
		assert.Equal(t, xdr.ScAddressTypeScAddressTypeContract, scAddr.Type)
		assert.NotNil(t, scAddr.ContractId)
		assert.Equal(t, validRaw, scAddr.ContractId[:])
	})

	t.Run("malformed contract ID", func(t *testing.T) {
		scAddr, err := ScAddressFromContractString("not-a-valid-contract-id")
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})

	t.Run("wrong length contract ID", func(t *testing.T) {
		shortRaw := make([]byte, 16)
		shortContractID, err := strkey.Encode(strkey.VersionByteContract, shortRaw)
		assert.NoError(t, err)

		scAddr, err := ScAddressFromContractString(shortContractID)
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})

	t.Run("wrong version byte", func(t *testing.T) {
		// Encode as an account ID instead of contract
		accountID, err := strkey.Encode(strkey.VersionByteAccountID, validRaw)
		assert.NoError(t, err)

		scAddr, err := ScAddressFromContractString(accountID)
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})
}

func TestScAddressFromString(t *testing.T) {
	// Prepare valid account ID
	validAccountRaw := make([]byte, 32)
	for i := range validAccountRaw {
		validAccountRaw[i] = byte(i)
	}
	validAccountID, err := strkey.Encode(strkey.VersionByteAccountID, validAccountRaw)
	assert.NoError(t, err)

	// Prepare valid contract ID
	validContractRaw := make([]byte, 32)
	for i := range validContractRaw {
		validContractRaw[i] = byte(255 - i)
	}
	validContractID, err := strkey.Encode(strkey.VersionByteContract, validContractRaw)
	assert.NoError(t, err)

	t.Run("valid account ID (G...)", func(t *testing.T) {
		scAddr, err := ScAddressFromString(validAccountID)
		assert.NoError(t, err)
		assert.NotNil(t, scAddr)
		assert.Equal(t, xdr.ScAddressTypeScAddressTypeAccount, scAddr.Type)
		assert.Equal(t, validAccountRaw, scAddr.AccountId.Ed25519[:])
	})

	t.Run("valid contract ID (C...)", func(t *testing.T) {
		scAddr, err := ScAddressFromString(validContractID)
		assert.NoError(t, err)
		assert.NotNil(t, scAddr)
		assert.Equal(t, xdr.ScAddressTypeScAddressTypeContract, scAddr.Type)
		assert.Equal(t, validContractRaw, scAddr.ContractId[:])
	})

	t.Run("unsupported prefix", func(t *testing.T) {
		scAddr, err := ScAddressFromString("X12345")
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})

	t.Run("invalid G address", func(t *testing.T) {
		scAddr, err := ScAddressFromString("G-invalid-key")
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})

	t.Run("invalid C address", func(t *testing.T) {
		scAddr, err := ScAddressFromString("C-invalid-key")
		assert.Error(t, err)
		assert.Nil(t, scAddr)
	})
}
