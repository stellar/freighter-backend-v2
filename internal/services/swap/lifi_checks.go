package swap

import (
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// lifiMaxFeeBps caps every fee LI.FI may route to itself or a partner.
const lifiMaxFeeBps = 25

// lifiExpect is what the reviewed quote promised; checkLifiTx refuses any
// transaction that does something else.
type lifiExpect struct {
	Account, FromContract, ToContract, SwapContract, Aggregator, FeeRecipient, PartnerID string
	// ToClassic allows ToContract.trust(Account) in the auth tree.
	ToClassic      bool
	Amount, MinOut *big.Int
	MaxValidUntil  int64
}

func checkFailed(what string) error { return fmt.Errorf("%s: %w", what, ErrCheckFailed) }

// checkLifiTx returns nil, ErrQuoteMoved (every other check passed but the
// minimum output is below what the user reviewed) or a wrapped ErrCheckFailed.
func checkLifiTx(env xdr.TransactionEnvelope, want lifiExpect) error {
	if want.Amount == nil || want.MinOut == nil {
		return checkFailed("expectation")
	}
	ic, err := checkLifiEnvelope(env, want)
	if err != nil {
		return err
	}
	call, err := decodeLifiCall(env)
	if err != nil {
		return checkFailed("swap params")
	}
	if err := checkLifiParams(call, want); err != nil {
		return err
	}
	ih := env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp
	if err := checkLifiAuth(ih.Auth, ic, want); err != nil {
		return err
	}
	tx := env.V1.Tx
	if tx.Ext.V != 1 || tx.Ext.SorobanData == nil ||
		tx.Ext.SorobanData.ResourceFee < 0 {
		return checkFailed("resource fee")
	}
	// Only plain time bounds: V2 preconditions can add signers or sequence rules.
	tb := tx.Cond.TimeBounds
	if tx.Cond.Type != xdr.PreconditionTypePrecondTime || tb == nil || tb.MaxTime == 0 ||
		want.MaxValidUntil <= 0 || uint64(tb.MaxTime) > uint64(want.MaxValidUntil) {
		return checkFailed("time bounds")
	}
	if call.MinAmountOut.Cmp(want.MinOut) < 0 {
		return ErrQuoteMoved
	}
	return nil
}

// checkLifiEnvelope runs checks 1 and 2 and returns the swap invocation.
func checkLifiEnvelope(env xdr.TransactionEnvelope, want lifiExpect) (xdr.InvokeContractArgs, error) {
	if env.Type != xdr.EnvelopeTypeEnvelopeTypeTx || env.V1 == nil {
		return xdr.InvokeContractArgs{}, checkFailed("envelope type")
	}
	tx := env.V1.Tx
	if src, err := tx.SourceAccount.GetAddress(); err != nil || src != want.Account {
		return xdr.InvokeContractArgs{}, checkFailed("tx source")
	}
	if tx.Memo.Type != xdr.MemoTypeMemoNone {
		return xdr.InvokeContractArgs{}, checkFailed("memo")
	}
	if len(tx.Operations) != 1 {
		return xdr.InvokeContractArgs{}, checkFailed("operation count")
	}
	op := tx.Operations[0]
	if op.SourceAccount != nil {
		if src, err := op.SourceAccount.GetAddress(); err != nil || src != want.Account {
			return xdr.InvokeContractArgs{}, checkFailed("operation source")
		}
	}
	ih, ok := op.Body.GetInvokeHostFunctionOp()
	if !ok {
		return xdr.InvokeContractArgs{}, checkFailed("operation type")
	}
	ic, ok := ih.HostFunction.GetInvokeContract()
	if !ok {
		return xdr.InvokeContractArgs{}, checkFailed("host function")
	}
	if addrString(ic.ContractAddress) != want.SwapContract {
		return xdr.InvokeContractArgs{}, checkFailed("swap contract")
	}
	if ic.FunctionName != "swap" {
		return xdr.InvokeContractArgs{}, checkFailed("swap function")
	}
	if len(ic.Args) != 2 || scAddr(ic.Args[1]) != want.Account {
		return xdr.InvokeContractArgs{}, checkFailed("receiver")
	}
	return ic, nil
}

// checkLifiParams is check 3: the swap parameters.
func checkLifiParams(call *lifiCall, want lifiExpect) error {
	params := call.Swap.Args[0]
	field := func(k string) string {
		v, _ := scField(params, k)
		return scAddr(v)
	}
	if field("token_in") != want.FromContract {
		return checkFailed("token_in")
	}
	if field("token_out") != want.ToContract {
		return checkFailed("token_out")
	}
	a := call.Args
	if scAddr(a[0]) != want.Account {
		return checkFailed("args account")
	}
	if scAddr(a[1]) != want.FromContract {
		return checkFailed("args token in")
	}
	if scAddr(a[3]) != want.ToContract {
		return checkFailed("args token out")
	}
	if argMin, ok := scI128(a[4]); !ok || argMin.Cmp(call.MinAmountOut) != 0 {
		return checkFailed("args min out")
	}
	for _, f := range call.Fees {
		dest, _ := scField(f, "fee_destination")
		if scAddr(dest) != want.FeeRecipient {
			return checkFailed("fee destination")
		}
		bpsVal, _ := scField(f, "fee_bps")
		bps, ok := scI128(bpsVal)
		if !ok || bps.Sign() < 0 || bps.Cmp(big.NewInt(lifiMaxFeeBps)) > 0 {
			return checkFailed("fee bps")
		}
	}
	partnerBps, _ := scField(a[6], "bps")
	if bps, ok := scU32(partnerBps); !ok || bps > lifiMaxFeeBps {
		return checkFailed("partner bps")
	}
	// The aggregator resolves the partner's fee recipient from this id.
	partnerID, _ := scField(a[6], "id")
	if sym, ok := partnerID.GetSym(); !ok || string(sym) != want.PartnerID {
		return checkFailed("partner id")
	}
	return nil
}

// checkLifiAuth is check 4: the authorization tree may move exactly Amount
// out of Account — a capped fee to FeeRecipient and the rest into the
// aggregator — and optionally open the destination trustline.
func checkLifiAuth(auth []xdr.SorobanAuthorizationEntry, ic xdr.InvokeContractArgs, want lifiExpect) error {
	if len(auth) != 1 {
		return checkFailed("auth entries")
	}
	if auth[0].Credentials.Type != xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount {
		return checkFailed("auth credentials")
	}
	root := auth[0].RootInvocation
	rootFn, ok := contractFn(root)
	if !ok {
		return checkFailed("auth root")
	}
	got, err1 := xdr.MarshalBase64(rootFn)
	op, err2 := xdr.MarshalBase64(ic)
	if err1 != nil || err2 != nil || got != op {
		return checkFailed("auth root")
	}

	feeCap := new(big.Int).Mul(want.Amount, big.NewInt(lifiMaxFeeBps))
	feeCap.Add(feeCap, big.NewInt(9999)).Quo(feeCap, big.NewInt(10000))
	fee, swapIn := new(big.Int), (*big.Int)(nil)
	var sawFee, sawTrust bool
	for _, child := range root.SubInvocations {
		fn, ok := contractFn(child)
		if !ok {
			return checkFailed("auth child")
		}
		contract, name := addrString(fn.ContractAddress), string(fn.FunctionName)
		switch {
		case contract == want.FromContract && name == "transfer":
			x, ok := transferAmount(fn, want.Account, want.FeeRecipient)
			if !ok || sawFee || len(child.SubInvocations) != 0 {
				return checkFailed("auth child")
			}
			if x.Sign() < 0 || x.Cmp(feeCap) > 0 {
				return checkFailed("auth fee")
			}
			fee, sawFee = x, true
		case contract == want.ToContract && name == "trust":
			if len(fn.Args) != 1 || scAddr(fn.Args[0]) != want.Account || sawTrust || len(child.SubInvocations) != 0 {
				return checkFailed("auth child")
			}
			if !want.ToClassic {
				return checkFailed("auth trust")
			}
			sawTrust = true
		case contract == want.Aggregator && name == "swap_exact_in":
			y, ok := aggregatorAmount(child, fn, want)
			if !ok || swapIn != nil {
				return checkFailed("auth aggregator")
			}
			swapIn = y
		default:
			return checkFailed("auth child")
		}
	}
	if swapIn == nil {
		return checkFailed("auth aggregator")
	}
	if new(big.Int).Add(fee, swapIn).Cmp(want.Amount) != 0 {
		return checkFailed("auth sum")
	}
	return nil
}

// aggregatorAmount validates Aggregator.swap_exact_in(Account, From, y, To, …)
// whose only child is From.transfer(Account, Aggregator, y), and returns y.
func aggregatorAmount(inv xdr.SorobanAuthorizedInvocation, fn xdr.InvokeContractArgs, want lifiExpect) (*big.Int, bool) {
	if len(fn.Args) < 4 || scAddr(fn.Args[0]) != want.Account || scAddr(fn.Args[1]) != want.FromContract ||
		scAddr(fn.Args[3]) != want.ToContract || len(inv.SubInvocations) != 1 {
		return nil, false
	}
	y, ok := scI128(fn.Args[2])
	if !ok || y.Sign() <= 0 {
		return nil, false
	}
	sub := inv.SubInvocations[0]
	subFn, ok := contractFn(sub)
	if !ok || addrString(subFn.ContractAddress) != want.FromContract || subFn.FunctionName != "transfer" ||
		len(sub.SubInvocations) != 0 {
		return nil, false
	}
	moved, ok := transferAmount(subFn, want.Account, want.Aggregator)
	if !ok || moved.Cmp(y) != 0 {
		return nil, false
	}
	return y, true
}

func contractFn(inv xdr.SorobanAuthorizedInvocation) (xdr.InvokeContractArgs, bool) {
	if inv.Function.Type != xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn ||
		inv.Function.ContractFn == nil {
		return xdr.InvokeContractArgs{}, false
	}
	return *inv.Function.ContractFn, true
}

// transferAmount reads transfer(from, to, amount).
func transferAmount(fn xdr.InvokeContractArgs, from, to string) (*big.Int, bool) {
	if len(fn.Args) != 3 || scAddr(fn.Args[0]) != from || scAddr(fn.Args[1]) != to {
		return nil, false
	}
	return scI128(fn.Args[2])
}
