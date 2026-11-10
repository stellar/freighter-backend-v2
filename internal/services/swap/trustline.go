package swap

import (
	"github.com/stellar/go-stellar-sdk/xdr"
)

// AddsTrustline reports whether the transaction opens a trustline for the
// account: a ChangeTrust operation (classic), or toContract.trust(account) in
// a Soroban authorization tree.
func AddsTrustline(txXDR, account, toContract string) (bool, error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(txXDR, &env); err != nil {
		return false, err
	}
	for _, op := range env.Operations() {
		if op.Body.Type == xdr.OperationTypeChangeTrust {
			return true, nil
		}
		if ih, ok := op.Body.GetInvokeHostFunctionOp(); ok && toContract != "" {
			for _, a := range ih.Auth {
				if hasTrustCall(a.RootInvocation, account, toContract) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func hasTrustCall(inv xdr.SorobanAuthorizedInvocation, account, toContract string) bool {
	if fn := inv.Function.ContractFn; fn != nil && string(fn.FunctionName) == "trust" &&
		addrString(fn.ContractAddress) == toContract && len(fn.Args) == 1 && scAddr(fn.Args[0]) == account {
		return true
	}
	for _, sub := range inv.SubInvocations {
		if hasTrustCall(sub, account, toContract) {
			return true
		}
	}
	return false
}
