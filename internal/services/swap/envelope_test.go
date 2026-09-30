package swap

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/utils"
)

const routerFunction = "execute_strategy"

func addrVal(a xdr.ScAddress) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
}

func i128Val(n int64) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(n)}}
}

func rootNode(t *testing.T, subs ...xdr.SorobanAuthorizedInvocation) xdr.SorobanAuthorizedInvocation {
	payload := xdr.ScBytes(goodPayload(t))
	return xdr.SorobanAuthorizedInvocation{
		Function: xdr.SorobanAuthorizedFunction{
			Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
			ContractFn: &xdr.InvokeContractArgs{
				ContractAddress: mustAddr(utils.ScAddressFromContractString(testContract(2))),
				FunctionName:    routerFunction,
				Args: []xdr.ScVal{
					addrVal(mustAddr(utils.ScAddressFromAccountString(testSender))),
					i128Val(100_0000000),
					{Type: xdr.ScValTypeScvBytes, Bytes: &payload},
				},
			},
		},
		SubInvocations: subs,
	}
}

func transferNode(token, from string, amount int64) xdr.SorobanAuthorizedInvocation {
	return xdr.SorobanAuthorizedInvocation{Function: xdr.SorobanAuthorizedFunction{
		Type: xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
		ContractFn: &xdr.InvokeContractArgs{
			ContractAddress: mustAddr(utils.ScAddressFromContractString(token)),
			FunctionName:    "transfer",
			Args:            []xdr.ScVal{addrVal(mustAddr(utils.ScAddressFromAccountString(from))), addrVal(mustAddr(utils.ScAddressFromContractString(testContract(2)))), i128Val(amount)},
		},
	}}
}

// testPayload builds the ScVal-encoded strategy payload the router expects:
// a struct {amounts, assets, ops} whose ops header names token_in, token_out and
// min_out by registry index.
type payloadOpts struct {
	version  byte
	referral byte
	// badIndex points token_in past the end of the asset registry; extraField
	// adds a struct field the router does not define.
	badIndex   bool
	extraField bool
	tokenIn    string
	tokenOut   string
	minOut     int64
}

func buildPayload(t *testing.T, o payloadOpts) []byte {
	t.Helper()
	header := []byte{o.version, 0, 1, 0, 0, 0, 0, o.referral, 0, 0}
	if o.badIndex {
		header[1] = 9
	}
	assets := []xdr.ScVal{addrVal(mustAddr(utils.ScAddressFromContractString(o.tokenIn))), addrVal(mustAddr(utils.ScAddressFromContractString(o.tokenOut)))}
	amounts := []xdr.ScVal{i128Val(o.minOut)}
	vec := func(v []xdr.ScVal) xdr.ScVal {
		sv := xdr.ScVec(v)
		p := &sv
		return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &p}
	}
	sym := func(s string) xdr.ScVal {
		x := xdr.ScSymbol(s)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &x}
	}
	ops := xdr.ScBytes(header)
	m := xdr.ScMap{
		{Key: sym("amounts"), Val: vec(amounts)},
		{Key: sym("assets"), Val: vec(assets)},
		{Key: sym("ops"), Val: xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &ops}},
	}
	if o.extraField {
		m = append(m, xdr.ScMapEntry{Key: sym("zzz"), Val: xdr.ScVal{Type: xdr.ScValTypeScvVoid}})
	}
	mp := &m
	out, err := (xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mp}).MarshalBinary()
	require.NoError(t, err)
	return out
}

func goodPayload(t *testing.T) []byte {
	return buildPayload(t, payloadOpts{version: 1, tokenIn: testContract(3), tokenOut: testContract(4), minOut: 990})
}

type envOpts struct {
	payload   []byte
	router    string
	function  xdr.ScSymbol
	sender    string
	amount    int64
	fee       xdr.Uint32
	signed    bool
	memo      bool
	muxed     bool
	opSource  bool
	extraOp   bool
	unsoroban bool
	auth      []xdr.SorobanAuthorizationEntry
}

func buildEnvelope(t *testing.T, o envOpts) string {
	t.Helper()
	payloadBytes := xdr.ScBytes(o.payload)
	if o.payload == nil {
		payloadBytes = xdr.ScBytes(goodPayload(t))
	}
	call := &xdr.InvokeContractArgs{
		ContractAddress: mustAddr(utils.ScAddressFromContractString(o.router)),
		FunctionName:    o.function,
		Args: []xdr.ScVal{
			addrVal(mustAddr(utils.ScAddressFromAccountString(o.sender))),
			i128Val(o.amount),
			{Type: xdr.ScValTypeScvBytes, Bytes: &payloadBytes},
		},
	}
	if o.auth == nil {
		root := rootNode(t)
		root.Function.ContractFn = call
		o.auth = []xdr.SorobanAuthorizationEntry{{
			Credentials:    xdr.SorobanCredentials{Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount},
			RootInvocation: root,
		}}
	}
	op := xdr.Operation{Body: xdr.OperationBody{
		Type: xdr.OperationTypeInvokeHostFunction,
		InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
			HostFunction: xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract, InvokeContract: call},
			Auth:         o.auth,
		},
	}}
	tx := xdr.Transaction{
		SourceAccount: xdr.MustMuxedAddress(testSender),
		Fee:           o.fee,
		Operations:    []xdr.Operation{op},
		Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
		Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
	}
	if !o.unsoroban {
		tx.Ext = xdr.TransactionExt{V: 1, SorobanData: &xdr.SorobanTransactionData{}}
	}
	if o.muxed {
		tx.SourceAccount = xdr.MuxedAccount{Type: xdr.CryptoKeyTypeKeyTypeMuxedEd25519, Med25519: &xdr.MuxedAccountMed25519{Id: 1, Ed25519: *xdr.MustAddress(testSender).Ed25519}}
	}
	if o.opSource {
		src := xdr.MustMuxedAddress(testSender)
		tx.Operations[0].SourceAccount = &src
	}
	if o.memo {
		text := "hi"
		tx.Memo = xdr.Memo{Type: xdr.MemoTypeMemoText, Text: &text}
	}
	if o.extraOp {
		tx.Operations = append(tx.Operations, op)
	}
	env := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTx, V1: &xdr.TransactionV1Envelope{Tx: tx}}
	if o.signed {
		env.V1.Signatures = []xdr.DecoratedSignature{{}}
	}
	out, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	return out
}

func decodeEnvelope(encoded string) (xdr.TransactionEnvelope, error) {
	var env xdr.TransactionEnvelope
	err := xdr.SafeUnmarshalBase64(encoded, &env)
	return env, err
}
