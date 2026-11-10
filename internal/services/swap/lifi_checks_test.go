package swap

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const (
	// The production pins, so the recorded mainnet fixtures check them.
	lifiSwapContract = LifiSwapContract
	lifiAggregator   = SoroswapAggregator
	lifiFeeRecipient = LifiFeeRecipient
	lifiPartnerID    = LifiPartnerID
	lifiTestAccount  = "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7"
	// EURC on mainnet: a contract the trust fixture does not touch.
	otherContract = "CDTKPWPLOURQA2SGTKTUQOWRCBZEORB4BWBOMJ3D3ZTQQSGE5F6JBQLV"
)

func trustFixtureXDR(t *testing.T) string {
	t.Helper()
	var q LifiQuote
	require.NoError(t, json.Unmarshal(lifiFixture(t, "lifi_quote_xlm_usdc_trust.json"), &q))
	require.NotNil(t, q.TransactionRequest)
	return q.TransactionRequest.Data
}

func decodeEnv(t *testing.T, b64 string) xdr.TransactionEnvelope {
	t.Helper()
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(b64, &env))
	return env
}

func contractIDOf(t *testing.T, id string) string {
	t.Helper()
	a, err := ParseAsset(id)
	require.NoError(t, err)
	c, err := a.ContractID(types.PUBLIC)
	require.NoError(t, err)
	return c
}

// trustExpect is the expectation the untouched trust fixture satisfies.
func trustExpect(t *testing.T, env xdr.TransactionEnvelope) lifiExpect {
	t.Helper()
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	require.NotNil(t, env.V1.Tx.Cond.TimeBounds)
	return lifiExpect{
		Account:      lifiTestAccount,
		FromContract: contractIDOf(t, "XLM"),
		ToContract:   contractIDOf(t, "USDC:"+usdcIssuer),
		SwapContract: lifiSwapContract,
		Aggregator:   lifiAggregator,
		FeeRecipient: lifiFeeRecipient,
		PartnerID:    lifiPartnerID,
		ToClassic:    true,
		Amount:       big.NewInt(100000000),
		MinOut:       new(big.Int).Set(call.MinAmountOut),
	}
}

func anyAddrVal(t *testing.T, s string) xdr.ScVal {
	t.Helper()
	a, err := utils.ScAddressFromString(s)
	require.NoError(t, err)
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: a}
}

func u32Val(n uint32) xdr.ScVal {
	u := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

func setField(t *testing.T, m xdr.ScVal, name string, val xdr.ScVal) {
	t.Helper()
	mp, ok := m.GetMap()
	require.True(t, ok && mp != nil)
	for i := range *mp {
		if scSym((*mp)[i].Key) == name {
			(*mp)[i].Val = val
			return
		}
	}
	t.Fatalf("no field %q", name)
}

func hostFn(env *xdr.TransactionEnvelope) *xdr.InvokeHostFunctionOp {
	return env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp
}

func swapParams(env *xdr.TransactionEnvelope) xdr.ScVal {
	return hostFn(env).HostFunction.InvokeContract.Args[0]
}

// syncRoot makes the auth root mirror the (tampered) operation invocation, so
// a tamper in the call is not caught by the root-equality check instead.
func syncRoot(env *xdr.TransactionEnvelope) {
	ih := hostFn(env)
	ic := *ih.HostFunction.InvokeContract
	ih.Auth[0].RootInvocation.Function.ContractFn = &ic
}

func rootChildren(env *xdr.TransactionEnvelope) *[]xdr.SorobanAuthorizedInvocation {
	return &hostFn(env).Auth[0].RootInvocation.SubInvocations
}

func childByFn(t *testing.T, env *xdr.TransactionEnvelope, fn string) *xdr.SorobanAuthorizedInvocation {
	t.Helper()
	kids := rootChildren(env)
	for i := range *kids {
		if c := (*kids)[i].Function.ContractFn; c != nil && string(c.FunctionName) == fn {
			return &(*kids)[i]
		}
	}
	t.Fatalf("no root child %q", fn)
	return nil
}

func contractCall(t *testing.T, contract, fn string, args ...xdr.ScVal) xdr.SorobanAuthorizedInvocation {
	t.Helper()
	a, err := utils.ScAddressFromString(contract)
	require.NoError(t, err)
	return xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
		Type:       xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
		ContractFn: &xdr.InvokeContractArgs{ContractAddress: *a, FunctionName: xdr.ScSymbol(fn), Args: args},
	}}
}

func muxed(t *testing.T, g string) xdr.MuxedAccount {
	t.Helper()
	m, err := xdr.AddressToMuxedAccount(g)
	require.NoError(t, err)
	return m
}

// runTamper decodes the fixture, applies mutate, round-trips through XDR and
// runs the checks.
func runTamper(t *testing.T, mutate func(env *xdr.TransactionEnvelope, want *lifiExpect)) error {
	t.Helper()
	env := decodeEnv(t, trustFixtureXDR(t))
	want := trustExpect(t, env)
	mutate(&env, &want)
	b64, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	var out error
	require.NotPanics(t, func() { out = checkLifiTx(decodeEnv(t, b64), want) })
	return out
}

func TestLifiChecksUntouchedPasses(t *testing.T) {
	assert.NoError(t, runTamper(t, func(*xdr.TransactionEnvelope, *lifiExpect) {}))
}

func TestLifiChecksMinOutAboveIsQuoteMoved(t *testing.T) {
	err := runTamper(t, func(_ *xdr.TransactionEnvelope, w *lifiExpect) { w.MinOut.Add(w.MinOut, big.NewInt(1)) })
	require.ErrorIs(t, err, ErrQuoteMoved)
	assert.NotErrorIs(t, err, ErrCheckFailed)
}

func TestLifiChecksTamperFails(t *testing.T) {
	other := keypair.MustRandom().Address()
	cases := []struct {
		name   string
		what   string // the failing check's label, so a later check cannot pass the test
		mutate func(t *testing.T, env *xdr.TransactionEnvelope, w *lifiExpect)
	}{
		// 1. envelope
		{"tx source changed", "tx source", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			e.V1.Tx.SourceAccount = muxed(t, other)
		}},
		{"memo added", "memo", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			m, err := xdr.NewMemo(xdr.MemoTypeMemoText, "x")
			require.NoError(t, err)
			e.V1.Tx.Memo = m
		}},
		{"second operation", "operation count", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			e.V1.Tx.Operations = append(e.V1.Tx.Operations, e.V1.Tx.Operations[0])
		}},
		{"operation source other", "operation source", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			m := muxed(t, other)
			e.V1.Tx.Operations[0].SourceAccount = &m
		}},
		// 2. invocation
		{"target is aggregator", "swap contract", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			a, err := utils.ScAddressFromString(lifiAggregator)
			require.NoError(t, err)
			hostFn(e).HostFunction.InvokeContract.ContractAddress = *a
			syncRoot(e)
		}},
		{"function renamed", "swap function", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			hostFn(e).HostFunction.InvokeContract.FunctionName = "swap2"
			syncRoot(e)
		}},
		{"receiver changed", "receiver", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			hostFn(e).HostFunction.InvokeContract.Args[1] = anyAddrVal(t, other)
			syncRoot(e)
		}},
		// 3. swap params
		{"token_in changed", "token_in", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			setField(t, swapParams(e), "token_in", anyAddrVal(t, otherContract))
			syncRoot(e)
		}},
		{"args[0] changed", "args account", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			scVec(mustField(t, swapParams(e), "args"))[0] = anyAddrVal(t, other)
			syncRoot(e)
		}},
		{"args[1] changed", "args token in", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			scVec(mustField(t, swapParams(e), "args"))[1] = anyAddrVal(t, otherContract)
			syncRoot(e)
		}},
		{"args[4] differs from min_amount_out", "args min out", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			scVec(mustField(t, swapParams(e), "args"))[4] = i128Val(1)
			syncRoot(e)
		}},
		{"token_out changed", "token_out", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			setField(t, swapParams(e), "token_out", anyAddrVal(t, otherContract))
			syncRoot(e)
		}},
		{"args[3] changed", "args token out", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			args := scVec(mustField(t, swapParams(e), "args"))
			args[3] = anyAddrVal(t, otherContract)
			syncRoot(e)
		}},
		{"fee destination changed", "fee destination", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			fees := scVec(mustField(t, swapParams(e), "fees"))
			require.NotEmpty(t, fees)
			setField(t, fees[0], "fee_destination", anyAddrVal(t, other))
			syncRoot(e)
		}},
		{"fee bps 26", "fee bps", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			fees := scVec(mustField(t, swapParams(e), "fees"))
			require.NotEmpty(t, fees)
			setField(t, fees[0], "fee_bps", i128Val(26))
			syncRoot(e)
		}},
		{"two fee entries of 25", "fee bps", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			vec := *mustField(t, swapParams(e), "fees").Vec
			require.NotEmpty(t, *vec)
			*vec = append(*vec, (*vec)[0])
			syncRoot(e)
		}},
		{"partner bps 26", "partner bps", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			args := scVec(mustField(t, swapParams(e), "args"))
			setField(t, args[6], "bps", u32Val(26))
			syncRoot(e)
		}},
		{"partner id changed", "partner id", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			args := scVec(mustField(t, swapParams(e), "args"))
			setField(t, args[6], "id", symVal("evilpartner"))
			syncRoot(e)
		}},
		// 4. auth
		{"address credentials", "auth credentials", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			a, err := utils.ScAddressFromString(lifiTestAccount)
			require.NoError(t, err)
			hostFn(e).Auth[0].Credentials = xdr.SorobanCredentials{
				Type: xdr.SorobanCredentialsTypeSorobanCredentialsAddress,
				Address: &xdr.SorobanAddressCredentials{
					Address: *a, Nonce: 1, SignatureExpirationLedger: 1,
					Signature: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
				},
			}
		}},
		{"extra root transfer", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, w *lifiExpect) {
			kids := rootChildren(e)
			*kids = append(*kids, contractCall(t, w.FromContract, "transfer",
				anyAddrVal(t, lifiTestAccount), anyAddrVal(t, other), i128Val(1)))
		}},
		{"trust other contract", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			trust := childByFn(t, e, "trust")
			*trust = contractCall(t, otherContract, "trust", anyAddrVal(t, lifiTestAccount))
		}},
		{"trust when not classic", "auth trust", func(_ *testing.T, _ *xdr.TransactionEnvelope, w *lifiExpect) {
			w.ToClassic = false
		}},
		{"aggregator amount raised", "auth sum", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			agg := childByFn(t, e, "swap_exact_in")
			y, ok := scI128(agg.Function.ContractFn.Args[2])
			require.True(t, ok)
			y.Add(y, big.NewInt(1))
			agg.Function.ContractFn.Args[2] = i128Val(y.Uint64())
			agg.SubInvocations[0].Function.ContractFn.Args[2] = i128Val(y.Uint64())
		}},
		{"aggregator child amount differs", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			agg := childByFn(t, e, "swap_exact_in")
			agg.SubInvocations[0].Function.ContractFn.Args[2] = i128Val(1)
		}},
		{"fee transfer above cap", "auth fee", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			fee := childByFn(t, e, "transfer")
			agg := childByFn(t, e, "swap_exact_in")
			// Move 1 XLM from the swap leg to the fee leg: sum unchanged.
			x, _ := scI128(fee.Function.ContractFn.Args[2])
			y, _ := scI128(agg.Function.ContractFn.Args[2])
			fee.Function.ContractFn.Args[2] = i128Val(x.Uint64() + 10000000)
			agg.Function.ContractFn.Args[2] = i128Val(y.Uint64() - 10000000)
			agg.SubInvocations[0].Function.ContractFn.Args[2] = i128Val(y.Uint64() - 10000000)
		}},
		{"root differs from operation", "auth root", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			root := hostFn(e).Auth[0].RootInvocation.Function.ContractFn
			root.FunctionName = "swap2"
		}},
		{"no auth", "auth entries", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			hostFn(e).Auth = nil
		}},
		{"two auth entries", "auth entries", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			hostFn(e).Auth = append(hostFn(e).Auth, hostFn(e).Auth[0])
		}},
		{"duplicate fee transfer", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			dup := *childByFn(t, e, "transfer")
			*rootChildren(e) = append(*rootChildren(e), dup)
		}},
		{"duplicate trust", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			dup := *childByFn(t, e, "trust")
			*rootChildren(e) = append(*rootChildren(e), dup)
		}},
		{"duplicate aggregator", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			dup := *childByFn(t, e, "swap_exact_in")
			*rootChildren(e) = append(*rootChildren(e), dup)
		}},
		{"trust wrong args", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "trust").Function.ContractFn.Args = []xdr.ScVal{anyAddrVal(t, other)}
		}},
		{"trust with child", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, w *lifiExpect) {
			childByFn(t, e, "trust").SubInvocations = []xdr.SorobanAuthorizedInvocation{
				contractCall(t, w.FromContract, "transfer", anyAddrVal(t, lifiTestAccount), anyAddrVal(t, other), i128Val(1)),
			}
		}},
		{"fee transfer with child", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, w *lifiExpect) {
			childByFn(t, e, "transfer").SubInvocations = []xdr.SorobanAuthorizedInvocation{
				contractCall(t, w.FromContract, "transfer", anyAddrVal(t, lifiTestAccount), anyAddrVal(t, other), i128Val(1)),
			}
		}},
		{"aggregator grandchild", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, w *lifiExpect) {
			agg := childByFn(t, e, "swap_exact_in")
			agg.SubInvocations[0].SubInvocations = []xdr.SorobanAuthorizedInvocation{
				contractCall(t, w.FromContract, "transfer", anyAddrVal(t, lifiTestAccount), anyAddrVal(t, other), i128Val(1)),
			}
		}},
		{"aggregator args[0] changed", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "swap_exact_in").Function.ContractFn.Args[0] = anyAddrVal(t, other)
		}},
		{"aggregator args[1] changed", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "swap_exact_in").Function.ContractFn.Args[1] = anyAddrVal(t, otherContract)
		}},
		{"aggregator args[3] changed", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "swap_exact_in").Function.ContractFn.Args[3] = anyAddrVal(t, otherContract)
		}},
		{"fee transfer to other", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "transfer").Function.ContractFn.Args[1] = anyAddrVal(t, other)
		}},
		{"fee transfer from other", "auth child", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "transfer").Function.ContractFn.Args[0] = anyAddrVal(t, other)
		}},
		{"aggregator transfer to other", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "swap_exact_in").SubInvocations[0].Function.ContractFn.Args[1] = anyAddrVal(t, other)
		}},
		{"aggregator transfer from other", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			childByFn(t, e, "swap_exact_in").SubInvocations[0].Function.ContractFn.Args[0] = anyAddrVal(t, other)
		}},
		{"aggregator transfer on other token", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			a, err := utils.ScAddressFromString(otherContract)
			require.NoError(t, err)
			childByFn(t, e, "swap_exact_in").SubInvocations[0].Function.ContractFn.ContractAddress = *a
		}},
		{"aggregator extra child", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			agg := childByFn(t, e, "swap_exact_in")
			agg.SubInvocations = append(agg.SubInvocations,
				contractCall(t, otherContract, "transfer", anyAddrVal(t, lifiTestAccount), anyAddrVal(t, other), i128Val(1)))
		}},
		{"aggregator amount zero", "auth aggregator", func(t *testing.T, e *xdr.TransactionEnvelope, w *lifiExpect) {
			// Amount 1 lets a fee of 1 stand for the whole sum, so only y > 0 rejects.
			w.Amount = big.NewInt(1)
			childByFn(t, e, "transfer").Function.ContractFn.Args[2] = i128Val(1)
			agg := childByFn(t, e, "swap_exact_in")
			agg.Function.ContractFn.Args[2] = i128Val(0)
			agg.SubInvocations[0].Function.ContractFn.Args[2] = i128Val(0)
		}},
		// 5. resource fee
		{"negative resource fee", "resource fee", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			e.V1.Tx.Ext.SorobanData.ResourceFee = -1
		}},
		{"precond v2", "time bounds", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			tb := *e.V1.Tx.Cond.TimeBounds
			e.V1.Tx.Cond = xdr.Preconditions{Type: xdr.PreconditionTypePrecondV2, V2: &xdr.PreconditionsV2{TimeBounds: &tb}}
		}},
		{"no time bounds", "time bounds", func(_ *testing.T, e *xdr.TransactionEnvelope, _ *lifiExpect) {
			e.V1.Tx.Cond = xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runTamper(t, func(e *xdr.TransactionEnvelope, w *lifiExpect) { tc.mutate(t, e, w) })
			require.ErrorIs(t, err, ErrCheckFailed)
			assert.Contains(t, err.Error(), tc.what)
		})
	}
}

// Hostile shapes must fail closed, never panic.
func TestLifiChecksHostileShapes(t *testing.T) {
	cases := map[string]func(t *testing.T, e *xdr.TransactionEnvelope){
		"arg0 void": func(_ *testing.T, e *xdr.TransactionEnvelope) {
			hostFn(e).HostFunction.InvokeContract.Args[0] = xdr.ScVal{Type: xdr.ScValTypeScvVoid}
			syncRoot(e)
		},
		"args vec short": func(t *testing.T, e *xdr.TransactionEnvelope) {
			setField(t, swapParams(e), "args", vecVal(anyAddrVal(t, lifiTestAccount)))
			syncRoot(e)
		},
		"fee entry not a map": func(t *testing.T, e *xdr.TransactionEnvelope) {
			setField(t, swapParams(e), "fees", vecVal(u32Val(1)))
			syncRoot(e)
		},
		"partner not a map": func(t *testing.T, e *xdr.TransactionEnvelope) {
			scVec(mustField(t, swapParams(e), "args"))[6] = u32Val(1)
			syncRoot(e)
		},
		"min out not i128": func(t *testing.T, e *xdr.TransactionEnvelope) {
			setField(t, swapParams(e), "min_amount_out", u32Val(1))
			syncRoot(e)
		},
		"root is create contract": func(_ *testing.T, e *xdr.TransactionEnvelope) {
			hostFn(e).Auth[0].RootInvocation.Function = xdr.SorobanAuthorizedFunction{
				Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractHostFn,
				CreateContractHostFn: &xdr.CreateContractArgs{
					ContractIdPreimage: xdr.ContractIdPreimage{Type: xdr.ContractIdPreimageTypeContractIdPreimageFromAsset, FromAsset: &xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}},
					Executable:         xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset},
				},
			}
		},
		"child transfer short args": func(t *testing.T, e *xdr.TransactionEnvelope) {
			childByFn(t, e, "transfer").Function.ContractFn.Args = nil
		},
		"aggregator no children": func(t *testing.T, e *xdr.TransactionEnvelope) {
			childByFn(t, e, "swap_exact_in").SubInvocations = nil
		},
		"aggregator short args": func(t *testing.T, e *xdr.TransactionEnvelope) {
			agg := childByFn(t, e, "swap_exact_in")
			agg.Function.ContractFn.Args = agg.Function.ContractFn.Args[:2]
		},
		"no soroban data": func(_ *testing.T, e *xdr.TransactionEnvelope) {
			e.V1.Tx.Ext = xdr.TransactionExt{V: 0}
		},
		"no time bounds": func(_ *testing.T, e *xdr.TransactionEnvelope) {
			e.V1.Tx.Cond = xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone}
		},
		"not an invoke": func(t *testing.T, e *xdr.TransactionEnvelope) {
			e.V1.Tx.Operations[0].Body = xdr.OperationBody{Type: xdr.OperationTypeBumpSequence, BumpSequenceOp: &xdr.BumpSequenceOp{}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			err := runTamper(t, func(e *xdr.TransactionEnvelope, _ *lifiExpect) { mutate(t, e) })
			assert.ErrorIs(t, err, ErrCheckFailed)
		})
	}
}

func TestLifiChecksNotV1Envelope(t *testing.T) {
	env := decodeEnv(t, trustFixtureXDR(t))
	want := trustExpect(t, env)
	var err error
	require.NotPanics(t, func() { err = checkLifiTx(xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTxV0}, want) })
	assert.ErrorIs(t, err, ErrCheckFailed)
}
