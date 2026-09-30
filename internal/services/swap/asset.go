package swap

import (
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
	"github.com/stellar/freighter-backend-v2/internal/utils/assetid"
)

// asset is a swap endpoint resolved from a client-supplied id. Exactly one
// of classic or contractID is set.
type asset struct {
	classic    *xdr.Asset
	contractID string
}

func (a asset) isClassic() bool { return a.classic != nil }

// parseAsset accepts "XLM", "CODE:ISSUER" or a Soroban contract id "C...".
func parseAsset(id string) (asset, error) {
	if utils.IsValidContractID(id) {
		return asset{contractID: id}, nil
	}
	canonical, err := assetid.Normalize(id)
	if err != nil {
		return asset{}, err
	}
	if canonical == assetid.NativeCanonical {
		native := xdr.MustNewNativeAsset()
		return asset{classic: &native}, nil
	}
	code, issuer, _ := strings.Cut(canonical, ":")
	credit, err := xdr.NewCreditAsset(code, issuer)
	if err != nil {
		return asset{}, fmt.Errorf("building asset %q: %w", canonical, err)
	}
	return asset{classic: &credit}, nil
}

func networkPassphrase(net string) (string, error) {
	switch net {
	case types.PUBLIC:
		return network.PublicNetworkPassphrase, nil
	case types.TESTNET:
		return network.TestNetworkPassphrase, nil
	default:
		return "", fmt.Errorf("%w: %s", errUnsupported, net)
	}
}

// contractIDFor returns the Soroban contract id that represents the asset on
// the given network: the id itself for a contract token, the derived Stellar
// Asset Contract for a classic asset.
func (a asset) contractIDFor(passphrase string) (string, error) {
	if !a.isClassic() {
		return a.contractID, nil
	}
	id, err := a.classic.ContractID(passphrase)
	if err != nil {
		return "", fmt.Errorf("deriving asset contract id: %w", err)
	}
	return strkey.Encode(strkey.VersionByteContract, id[:])
}
