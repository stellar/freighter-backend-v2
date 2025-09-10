package utils

import (
	"fmt"
	"strings"

	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
)

func IsValidContractID(s string) bool {
	decoded, err := strkey.Decode(strkey.VersionByteContract, s)
	return err == nil && len(decoded) == 32
}

func IsValidStellarPublicKey(s string) bool {
	decoded, err := strkey.Decode(strkey.VersionByteAccountID, s)
	return err == nil && len(decoded) == 32
}

func IsValidAccount(s string) bool {
	return IsValidStellarPublicKey(s) || IsValidContractID(s)
}

func ScAddressFromAccountString(address string) (*xdr.ScAddress, error) {
	raw, err := strkey.Decode(strkey.VersionByteAccountID, address)
	if err != nil {
		return nil, fmt.Errorf("invalid account ID: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("decoded account ID should be 32 bytes, got %d", len(raw))
	}

	var ed25519 xdr.Uint256
	copy(ed25519[:], raw)

	return &xdr.ScAddress{
		Type: xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &xdr.AccountId{
			Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
			Ed25519: &ed25519,
		},
	}, nil
}

func ScAddressFromContractString(address string) (*xdr.ScAddress, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, address)
	if err != nil {
		return nil, fmt.Errorf("invalid contract ID: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("decoded contract ID should be 32 bytes, got %d", len(raw))
	}

	var hash xdr.Hash
	copy(hash[:], raw)

	return &xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &hash,
	}, nil
}

func ScAddressFromString(addr string) (*xdr.ScAddress, error) {
	switch {
	case strings.HasPrefix(addr, "G"):
		return ScAddressFromAccountString(addr)
	case strings.HasPrefix(addr, "C"):
		return ScAddressFromContractString(addr)
	default:
		return nil, fmt.Errorf("unsupported address prefix")
	}
}

func ScVecToStrings(vec *xdr.ScVec) ([]string, error) {
	if vec == nil {
		return []string{}, nil
	}

	scvals := *vec
	members := make([]string, len(scvals))

	for i, v := range scvals {
		if v.Type != xdr.ScValTypeScvU32 || v.U32 == nil {
			return nil, fmt.Errorf("unexpected element type at index %d: %v", i, v.Type)
		}
		members[i] = fmt.Sprintf("%d", *v.U32)
	}

	return members, nil
}
