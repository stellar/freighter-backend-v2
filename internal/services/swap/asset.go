package swap

import (
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

var errBadAsset = errors.New("invalid asset")

// Asset is a parsed swap asset. ID is the canonical request form: "XLM",
// "CODE:ISSUER", or a Soroban contract address ("C...").
type Asset struct {
	ID       string
	Code     string
	Issuer   string
	Contract string
}

func ParseAsset(s string) (Asset, error) {
	switch {
	case s == "XLM":
		return Asset{ID: "XLM"}, nil
	case strkey.IsValidContractAddress(s):
		return Asset{ID: s, Contract: s}, nil
	}
	// SEP-11 "CODE:ISSUER". Its "native" spelling is not part of this API.
	ta, err := txnbuild.ParseAssetString(s)
	if err != nil {
		return Asset{}, fmt.Errorf("%w: %q", errBadAsset, s)
	}
	ca, ok := ta.(txnbuild.CreditAsset)
	if !ok {
		return Asset{}, fmt.Errorf("%w: %q", errBadAsset, s)
	}
	return Asset{ID: s, Code: ca.Code, Issuer: ca.Issuer}, nil
}

func (a Asset) IsNative() bool  { return a.ID == "XLM" }
func (a Asset) IsClassic() bool { return a.Contract == "" }

func (a Asset) TxnbuildAsset() (txnbuild.Asset, error) {
	switch {
	case a.IsNative():
		return txnbuild.NativeAsset{}, nil
	case a.IsClassic():
		return txnbuild.CreditAsset{Code: a.Code, Issuer: a.Issuer}, nil
	}
	return nil, fmt.Errorf("%w: %s is not a classic asset", errBadAsset, a.ID)
}

func (a Asset) XDR() (xdr.Asset, error) {
	ta, err := a.TxnbuildAsset()
	if err != nil {
		return xdr.Asset{}, err
	}
	return ta.ToXDR()
}

// ContractID returns the asset's Soroban contract ID: the Stellar Asset
// Contract for classic assets, the ID itself otherwise.
func (a Asset) ContractID(net string) (string, error) {
	if !a.IsClassic() {
		return a.Contract, nil
	}
	pass, err := Passphrase(net)
	if err != nil {
		return "", err
	}
	x, err := a.XDR()
	if err != nil {
		return "", err
	}
	id, err := x.ContractID(pass)
	if err != nil {
		return "", err
	}
	return strkey.Encode(strkey.VersionByteContract, id[:])
}

func Passphrase(net string) (string, error) {
	switch net {
	case types.PUBLIC:
		return network.PublicNetworkPassphrase, nil
	case types.TESTNET:
		return network.TestNetworkPassphrase, nil
	case types.FUTURENET:
		return network.FutureNetworkPassphrase, nil
	}
	return "", fmt.Errorf("unknown network %q", net)
}
