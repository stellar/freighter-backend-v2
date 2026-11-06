package swap

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

func fixtureEnvelope(t *testing.T, name string) (xdr.TransactionEnvelope, LifiQuote) {
	t.Helper()
	var q LifiQuote
	require.NoError(t, json.Unmarshal(lifiFixture(t, name), &q))
	require.NotNil(t, q.TransactionRequest)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(q.TransactionRequest.Data, &env))
	return env, q
}

type routeCase struct {
	from, to                 Asset
	fromContract, toContract string
}

func xlmTo(t *testing.T, to string) routeCase {
	t.Helper()
	from, err := ParseAsset("XLM")
	require.NoError(t, err)
	toA, err := ParseAsset(to)
	require.NoError(t, err)
	fc, err := from.ContractID(types.PUBLIC)
	require.NoError(t, err)
	tc, err := toA.ContractID(types.PUBLIC)
	require.NoError(t, err)
	return routeCase{from, toA, fc, tc}
}

// fixturePaths decodes a fixture and runs lifiPaths for XLM → to.
func fixturePaths(t *testing.T, name, to string, m *metrics.Swap) ([]types.SwapPath, routeCase) {
	t.Helper()
	env, _ := fixtureEnvelope(t, name)
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	rc := xlmTo(t, to)
	return lifiPaths(call, rc.from, rc.to, rc.fromContract, rc.toContract, m), rc
}

func shareSum(t *testing.T, paths []types.SwapPath) *big.Rat {
	t.Helper()
	sum := new(big.Rat)
	for _, p := range paths {
		r, ok := new(big.Rat).SetString(p.Share)
		require.True(t, ok, p.Share)
		sum.Add(sum, r)
	}
	return sum
}

func assertNearOne(t *testing.T, sum *big.Rat) {
	t.Helper()
	diff := new(big.Rat).Sub(sum, big.NewRat(1, 1))
	diff.Abs(diff)
	assert.True(t, diff.Cmp(big.NewRat(2, 10000)) <= 0, "shares sum to %s", sum.FloatString(6))
}

func assertChained(t *testing.T, p types.SwapPath) {
	t.Helper()
	for i := 0; i+1 < len(p.Hops); i++ {
		assert.Equal(t, p.Hops[i].To, p.Hops[i+1].From, "hop %d", i)
	}
}

func TestLifiRouteDecodeCall(t *testing.T) {
	env, q := fixtureEnvelope(t, "lifi_quote_xlm_usdc_trust.json")
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	assert.Equal(t, "swap", string(call.Swap.FunctionName))
	assert.Len(t, call.Args, 7)
	assert.NotEmpty(t, call.Fees)
	assert.Equal(t, q.Estimate.ToAmountMin, call.MinAmountOut.String())
	var fixture struct {
		Action struct {
			FromAddress string `json:"fromAddress"`
		} `json:"action"`
	}
	require.NoError(t, json.Unmarshal(lifiFixture(t, "lifi_quote_xlm_usdc_trust.json"), &fixture))
	assert.Equal(t, fixture.Action.FromAddress, call.Receiver)
}

func TestLifiRouteDecodeCallRejectsOtherShapes(t *testing.T) {
	env, _ := fixtureEnvelope(t, "lifi_quote_xlm_usdc_trust.json")
	_, err := decodeLifiCall(xdr.TransactionEnvelope{})
	assert.Error(t, err)

	noArgs := cloneEnvelope(t, env)
	invokeArgs(t, &noArgs).Args = nil
	_, err = decodeLifiCall(noArgs)
	assert.Error(t, err)

	notMap := cloneEnvelope(t, env)
	invokeArgs(t, &notMap).Args[0] = xdr.ScVal{Type: xdr.ScValTypeScvVoid}
	_, err = decodeLifiCall(notMap)
	assert.Error(t, err)
}

func TestLifiRouteTrustFixture(t *testing.T) {
	usdc := "USDC:" + usdcIssuer
	paths, rc := fixturePaths(t, "lifi_quote_xlm_usdc_trust.json", usdc, nil)
	require.Equal(t, "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75", rc.toContract)

	env, _ := fixtureEnvelope(t, "lifi_quote_xlm_usdc_trust.json")
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	require.Len(t, paths, len(scVec(call.Args[5])))
	assertNearOne(t, shareSum(t, paths))
	for _, p := range paths {
		require.NotEmpty(t, p.Hops)
		for _, h := range p.Hops {
			assert.Contains(t, []string{"Aquarius", "SushiSwap", "Soroswap"}, h.Venue)
			assert.NotEmpty(t, h.Pool)
		}
		assert.Equal(t, "XLM", p.Hops[0].From)
		assert.Equal(t, usdc, p.Hops[len(p.Hops)-1].To)
		assertChained(t, p)
	}
}

func TestLifiRouteSplitFixture(t *testing.T) {
	// EURC's issuer is not needed: toContract only relabels the final hop.
	env, _ := fixtureEnvelope(t, "lifi_quote_xlm_eurc_split.json")
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	rc := xlmTo(t, "USDC:"+usdcIssuer)
	toContract := scAddr(call.Args[3])
	paths := lifiPaths(call, rc.from, Asset{ID: toContract, Contract: toContract}, rc.fromContract, toContract, nil)

	require.GreaterOrEqual(t, len(paths), 2)
	assertNearOne(t, shareSum(t, paths))
	var soroswapPool, aquaChained bool
	for _, p := range paths {
		assertChained(t, p)
		for _, h := range p.Hops {
			if h.Venue == "Soroswap" && h.Pool != "" {
				soroswapPool = true
			}
			if h.Venue == "Aquarius" && len(p.Hops) >= 2 {
				aquaChained = true
				assert.True(t, strings.HasSuffix(h.Pool, "…"), h.Pool)
				assert.Len(t, strings.TrimSuffix(h.Pool, "…"), 8)
			}
		}
	}
	assert.True(t, soroswapPool, "a Soroswap hop with a pool")
	assert.True(t, aquaChained, "an Aquarius path of 2+ chained hops")
}

func TestLifiRoute100kFixture(t *testing.T) {
	paths, _ := fixturePaths(t, "lifi_quote_xlm_usdc_100k.json", "USDC:"+usdcIssuer, nil)
	var sushi bool
	for _, p := range paths {
		for _, h := range p.Hops {
			if h.Venue == "SushiSwap" && strkey.IsValidContractAddress(h.Pool) {
				sushi = true
			}
		}
	}
	assert.True(t, sushi, "a SushiSwap hop with a C… pool")
}

func TestLifiRouteShareFormatting(t *testing.T) {
	assert.Equal(t, "0.7", shareString(big.NewInt(7), big.NewInt(10)))
	assert.Equal(t, "1", shareString(big.NewInt(3), big.NewInt(3)))
	assert.Equal(t, "0.3333", shareString(big.NewInt(1), big.NewInt(3)))
}

func TestLifiRouteAquaMultiStep(t *testing.T) {
	rc := xlmTo(t, "USDC:"+usdcIssuer)
	mid := "CBEM2CAIYLM3HBOPU5HLQL7V5BUAKM3N77DYQKX4FNHTQLQUUD2ZFBOX"
	hash := make([]byte, 32)
	hash[0] = 0xab
	step := func(a, b string) xdr.ScVal {
		return vecVal(vecVal(addrVal(t, a), addrVal(t, b)), bytesVal(hash), addrVal(t, b))
	}
	hop := vecVal(symVal("Aqua"), mapVal(
		"steps", vecVal(step(rc.fromContract, mid), step(mid, rc.toContract)),
		"token_in", addrVal(t, rc.fromContract),
	))
	call := &lifiCall{Args: distArgs(mapVal("amount_in", i128Val(5), "hops", vecVal(hop)))}
	paths := lifiPaths(call, rc.from, rc.to, rc.fromContract, rc.toContract, nil)
	require.Len(t, paths, 1)
	assert.Equal(t, []types.SwapHop{
		{Venue: "Aquarius", From: "XLM", To: mid, Pool: "ab000000…"},
		{Venue: "Aquarius", From: mid, To: rc.to.ID, Pool: "ab000000…"},
	}, paths[0].Hops)
}

func TestLifiRouteUnknownVenueFallsBack(t *testing.T) {
	env, _ := fixtureEnvelope(t, "lifi_quote_xlm_usdc_trust.json")
	mod := cloneEnvelope(t, env)
	call, err := decodeLifiCall(mod)
	require.NoError(t, err)
	hop0 := scVec(scVec(mustField(t, scVec(call.Args[5])[0], "hops"))[0])
	*hop0[0].Sym = "Mystery"

	// Re-encode so the change goes through decodeLifiCall, not just the struct.
	b64, err := xdr.MarshalBase64(mod)
	require.NoError(t, err)
	var back xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(b64, &back))
	call, err = decodeLifiCall(back)
	require.NoError(t, err)

	m := metrics.NewSwap(prometheus.NewRegistry())
	rc := xlmTo(t, "USDC:"+usdcIssuer)
	paths := lifiPaths(call, rc.from, rc.to, rc.fromContract, rc.toContract, m)
	assert.Equal(t, []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "Soroswap", From: "XLM", To: rc.to.ID}}}}, paths)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.UnknownHops.WithLabelValues("other")))
}

func TestLifiRouteMalformedShapesNeverPanic(t *testing.T) {
	rc := xlmTo(t, "USDC:"+usdcIssuer)
	fallback := []types.SwapPath{{Share: "1", Hops: []types.SwapHop{{Venue: "Soroswap", From: "XLM", To: rc.to.ID}}}}
	void := xdr.ScVal{Type: xdr.ScValTypeScvVoid}
	nilVec := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: new(*xdr.ScVec)} // decoded absent optional
	cases := map[string]*lifiCall{
		"nil call":          nil,
		"short args":        {Args: []xdr.ScVal{void}},
		"distribution void": {Args: distArgs()[:5]},
		"nil vec":           {Args: append(distArgs()[:5], nilVec, void)},
		"empty":             {Args: distArgs()},
		"entry not map":     {Args: distArgs(void)},
		"zero amount":       {Args: distArgs(mapVal("amount_in", i128Val(0), "hops", vecVal(vecVal(symVal("Soroswap"), void))))},
		"hop body void":     {Args: distArgs(mapVal("amount_in", i128Val(1), "hops", vecVal(vecVal(symVal("Soroswap"), void))))},
		"hop not vec":       {Args: distArgs(mapVal("amount_in", i128Val(1), "hops", vecVal(void)))},
		"aqua bad hash":     {Args: distArgs(mapVal("amount_in", i128Val(1), "hops", vecVal(vecVal(symVal("Aqua"), mapVal("steps", vecVal(vecVal(void, bytesVal([]byte{1}), addrVal(t, rc.toContract))), "token_in", addrVal(t, rc.fromContract))))))},
		"sushi no pool":     {Args: distArgs(mapVal("amount_in", i128Val(1), "hops", vecVal(vecVal(symVal("Sushi"), mapVal("token_in", addrVal(t, rc.fromContract), "token_out", addrVal(t, rc.toContract))))))},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var paths []types.SwapPath
			require.NotPanics(t, func() { paths = lifiPaths(c, rc.from, rc.to, rc.fromContract, rc.toContract, nil) })
			assert.Equal(t, fallback, paths)
		})
	}
}

// --- ScVal builders for synthetic cases ---

func distArgs(entries ...xdr.ScVal) []xdr.ScVal {
	void := xdr.ScVal{Type: xdr.ScValTypeScvVoid}
	return []xdr.ScVal{void, void, void, void, void, vecVal(entries...), void}
}

func vecVal(vs ...xdr.ScVal) xdr.ScVal {
	v := xdr.ScVec(vs)
	p := &v
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &p}
}

func mapVal(kv ...any) xdr.ScVal {
	var m xdr.ScMap
	for i := 0; i < len(kv); i += 2 {
		m = append(m, xdr.ScMapEntry{Key: symVal(kv[i].(string)), Val: kv[i+1].(xdr.ScVal)})
	}
	p := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &p}
}

func symVal(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func bytesVal(b []byte) xdr.ScVal {
	sb := xdr.ScBytes(b)
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &sb}
}

func i128Val(n uint64) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Lo: xdr.Uint64(n)}}
}

func addrVal(t *testing.T, c string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, c)
	require.NoError(t, err)
	var id xdr.ContractId
	copy(id[:], raw)
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}}
}

func mustField(t *testing.T, v xdr.ScVal, name string) xdr.ScVal {
	t.Helper()
	f, ok := scField(v, name)
	require.True(t, ok, name)
	return f
}

func cloneEnvelope(t *testing.T, env xdr.TransactionEnvelope) xdr.TransactionEnvelope {
	t.Helper()
	b64, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	var out xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(b64, &out))
	return out
}

func invokeArgs(t *testing.T, env *xdr.TransactionEnvelope) *xdr.InvokeContractArgs {
	t.Helper()
	ic := env.V1.Tx.Operations[0].Body.InvokeHostFunctionOp.HostFunction.InvokeContract
	require.NotNil(t, ic)
	return ic
}

func TestLifiRouteScalarHelpers(t *testing.T) {
	neg := xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Hi: -1, Lo: xdr.Uint64(^uint64(0) - 4)}}
	n, ok := scI128(neg)
	require.True(t, ok)
	assert.Equal(t, "-5", n.String())
	big128 := xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Hi: 1, Lo: 2}}
	n, ok = scI128(big128)
	require.True(t, ok)
	assert.Equal(t, "18446744073709551618", n.String())
	_, ok = scI128(symVal("x"))
	assert.False(t, ok)

	env, _ := fixtureEnvelope(t, "lifi_quote_xlm_usdc_trust.json")
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	bps, ok := scU32(mustField(t, call.Args[6], "bps"))
	require.True(t, ok)
	assert.Equal(t, uint32(25), bps)
	_, ok = scU32(symVal("x"))
	assert.False(t, ok)
}
