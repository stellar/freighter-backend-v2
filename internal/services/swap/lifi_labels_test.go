package swap

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

// EURC (Circle) mainnet SAC; here it stands in for any contract that is not USDC.
const eurcContract = "CDTKPWPLOURQA2SGTKTUQOWRCBZEORB4BWBOMJ3D3ZTQQSGE5F6JBQLV"

func strVal(s string) *xdr.ScVal {
	str := xdr.ScString(s)
	return &xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
}

func namesOf(names map[string]string) func(string) (*xdr.ScVal, error) {
	return func(contract string) (*xdr.ScVal, error) {
		if n, ok := names[contract]; ok {
			return strVal(n), nil
		}
		return nil, &metrics.UpstreamError{Kind: "simulation_error", Err: errors.New("no name()")}
	}
}

func hopTokens(paths []types.SwapPath) []string {
	var out []string
	for _, p := range paths {
		for _, h := range p.Hops {
			out = append(out, h.From, h.To)
		}
	}
	return out
}

func TestLifiLabelsMiddleSACInSplitFixture(t *testing.T) {
	env, _ := fixtureEnvelope(t, "lifi_quote_xlm_eurc_split.json")
	call, err := decodeLifiCall(env)
	require.NoError(t, err)
	rc := xlmTo(t, "USDC:"+usdcIssuer)
	toContract := scAddr(call.Args[3])
	to := Asset{ID: toContract, Contract: toContract}
	paths := lifiPaths(call, rc.from, to, rc.fromContract, toContract, nil)
	require.Contains(t, hopTokens(paths), usdcContract, "the fixture routes through USDC")

	rpc := &fakeDecimalsRPC{names: namesOf(map[string]string{usdcContract: "USDC:" + usdcIssuer})}
	NewLifiSource(LifiConfig{RPC: rpc}, nil).labelSACHops(context.Background(), "GACCOUNT", types.PUBLIC, rc.from, to, paths)

	tokens := hopTokens(paths)
	assert.NotContains(t, tokens, usdcContract)
	assert.Contains(t, tokens, "USDC:"+usdcIssuer)
	assert.Contains(t, tokens, toContract, "the bought token keeps the ID it was asked for")
	for _, p := range paths {
		assertChained(t, p)
	}
	assert.Equal(t, 1, rpc.nameCalls[usdcContract])
}

func TestLifiLabelsOnlyVerifiedSACs(t *testing.T) {
	from, to := Asset{ID: "XLM"}, Asset{ID: "BTKN", Contract: "BTKN"}
	hops := func() []types.SwapPath {
		return []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
			{Venue: "Aquarius", From: "XLM", To: eurcContract},
			{Venue: "Soroswap", From: eurcContract, To: usdcContract},
			{Venue: "Soroswap", From: usdcContract, To: "BTKN"},
		}}}
	}
	t.Run("a name that does not derive back to the contract is not trusted", func(t *testing.T) {
		rpc := &fakeDecimalsRPC{names: namesOf(map[string]string{eurcContract: "USDC:" + usdcIssuer})}
		paths := hops()
		NewLifiSource(LifiConfig{RPC: rpc}, nil).labelSACHops(context.Background(), "GACCOUNT", types.PUBLIC, from, to, paths)
		assert.Equal(t, eurcContract, paths[0].Hops[0].To)
		assert.Equal(t, usdcContract, paths[0].Hops[2].From, "no name() keeps the contract ID")
	})
	t.Run("native", func(t *testing.T) {
		native, err := Asset{ID: "XLM"}.ContractID(types.PUBLIC)
		require.NoError(t, err)
		rpc := &fakeDecimalsRPC{names: namesOf(map[string]string{native: "native", usdcContract: "USDC:" + usdcIssuer})}
		paths := []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
			{Venue: "Soroswap", From: usdcContract, To: native},
			{Venue: "Soroswap", From: native, To: "BTKN"},
		}}}
		NewLifiSource(LifiConfig{RPC: rpc}, nil).labelSACHops(context.Background(), "GACCOUNT", types.PUBLIC, Asset{ID: usdcContract, Contract: usdcContract}, to, paths)
		assert.Equal(t, "XLM", paths[0].Hops[0].To)
		assert.Equal(t, usdcContract, paths[0].Hops[0].From, "the sold token keeps the ID it was asked for")
	})
	t.Run("a failed RPC call is retried on the next quote; an answer is cached", func(t *testing.T) {
		fail := true
		rpc := &fakeDecimalsRPC{names: func(c string) (*xdr.ScVal, error) {
			if fail {
				return nil, errors.New("connection reset")
			}
			return namesOf(map[string]string{usdcContract: "USDC:" + usdcIssuer})(c)
		}}
		src := NewLifiSource(LifiConfig{RPC: rpc}, nil)
		paths := hops()
		src.labelSACHops(context.Background(), "GACCOUNT", types.PUBLIC, from, to, paths)
		assert.Equal(t, usdcContract, paths[0].Hops[1].To)

		fail = false
		for range 2 {
			paths = hops()
			src.labelSACHops(context.Background(), "GACCOUNT", types.PUBLIC, from, to, paths)
			assert.Equal(t, "USDC:"+usdcIssuer, paths[0].Hops[1].To)
		}
		assert.Equal(t, 2, rpc.nameCalls[usdcContract], "one failed call, one answer, then the cache")
		assert.Equal(t, 2, rpc.nameCalls[eurcContract], "a contract with no name() is cached as unlabelled after the first answer")
	})
}

func TestLifiQuoteLabelsMiddleSACs(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_eurc_split.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	rpc := &fakeDecimalsRPC{fn: decimalsOf(7), names: namesOf(map[string]string{usdcContract: "USDC:" + usdcIssuer})}
	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	req.ToAsset = eurcContract

	rt, err := lifiServerRPC(t, 200, body, rpc).Quote(context.Background(), req)
	require.NoError(t, err)
	tokens := hopTokens(rt.Paths)
	assert.Contains(t, tokens, "USDC:"+usdcIssuer)
	assert.NotContains(t, tokens, usdcContract)
	assert.Contains(t, tokens, eurcContract, "the bought token keeps the ID it was asked for")
}

// blockingNameRPC never answers name(); it returns only when ctx ends.
type blockingNameRPC struct{ utils.MockRPCService }

func (*blockingNameRPC) SimulateInvocation(ctx context.Context, _ xdr.ScAddress, _ *txnbuild.SimpleAccount, _ xdr.ScSymbol,
	_ []xdr.ScVal, _ txnbuild.TimeBounds, _ string,
) (types.SimulateTransactionResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestLifiLabelsFinishBeforeTheQuoteDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	paths := []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
		{Venue: "Soroswap", From: "XLM", To: usdcContract},
		{Venue: "Soroswap", From: usdcContract, To: "BTKN"},
	}}}
	NewLifiSource(LifiConfig{RPC: &blockingNameRPC{}}, nil).labelSACHops(ctx, "GACCOUNT", types.PUBLIC, Asset{ID: "XLM"}, Asset{ID: "BTKN", Contract: "BTKN"}, paths)
	require.NoError(t, ctx.Err(), "labelling ran into the quote's deadline, so the router would drop the route")
	assert.Equal(t, usdcContract, paths[0].Hops[0].To)
}
