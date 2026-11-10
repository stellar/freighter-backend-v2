package swap

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stellar/go-stellar-sdk/ingest/sac"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
)

// EURC (Circle) mainnet SAC; here it stands in for any contract that is not USDC.
const eurcContract = "CDTKPWPLOURQA2SGTKTUQOWRCBZEORB4BWBOMJ3D3ZTQQSGE5F6JBQLV"

// instanceEntries answers GetLedgerEntriesXDR with a SAC instance entry for
// each contract in assets, built as if that contract held that classic asset
// ("CODE:ISSUER"). A contract not in assets has no entry.
func instanceEntries(t *testing.T, assets map[string]string) func([]string) (map[string]string, error) {
	t.Helper()
	return func(keys []string) (map[string]string, error) {
		out := map[string]string{}
		for _, k := range keys {
			var key xdr.LedgerKey
			require.NoError(t, xdr.SafeUnmarshalBase64(k, &key))
			contract := addrString(key.ContractData.Contract)
			a, ok := assets[contract]
			if !ok {
				continue
			}
			parsed, err := ParseAsset(a)
			require.NoError(t, err)
			raw, err := strkey.Decode(strkey.VersionByteContract, contract)
			require.NoError(t, err)
			var id [32]byte
			copy(id[:], raw)
			data, err := sac.AssetToContractData(false, parsed.Code, parsed.Issuer, id)
			require.NoError(t, err)
			b64, err := xdr.MarshalBase64(data)
			require.NoError(t, err)
			out[k] = b64
		}
		return out, nil
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

var (
	labelFrom = Asset{ID: "XLM"}
	labelTo   = Asset{ID: "BTKN", Contract: "BTKN"}
)

func middleHops() []types.SwapPath {
	return []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
		{Venue: "Aquarius", From: "XLM", To: eurcContract},
		{Venue: "Soroswap", From: eurcContract, To: usdcContract},
		{Venue: "Soroswap", From: usdcContract, To: "BTKN"},
	}}}
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

	rpc := &fakeDecimalsRPC{ledger: instanceEntries(t, map[string]string{usdcContract: "USDC:" + usdcIssuer})}
	NewLifiSource(LifiConfig{RPC: rpc}, nil).labelSACHops(context.Background(), types.PUBLIC, rc.from, to, paths)

	tokens := hopTokens(paths)
	assert.NotContains(t, tokens, usdcContract)
	assert.Contains(t, tokens, "USDC:"+usdcIssuer)
	assert.Contains(t, tokens, toContract, "the bought token keeps the ID it was asked for")
	for _, p := range paths {
		assertChained(t, p)
	}
	assert.Equal(t, 1, rpc.ledgerReads(), "one batched read for every middle token")
}

func TestLifiLabelsOnlyVerifiedSACs(t *testing.T) {
	t.Run("an instance whose asset does not derive back to the contract is not trusted", func(t *testing.T) {
		rpc := &fakeDecimalsRPC{ledger: instanceEntries(t, map[string]string{eurcContract: "USDC:" + usdcIssuer})}
		paths := middleHops()
		NewLifiSource(LifiConfig{RPC: rpc}, nil).labelSACHops(context.Background(), types.PUBLIC, labelFrom, labelTo, paths)
		assert.Equal(t, eurcContract, paths[0].Hops[0].To)
		assert.Equal(t, usdcContract, paths[0].Hops[2].From, "no instance entry keeps the contract ID")
	})
	t.Run("native is XLM, with no read", func(t *testing.T) {
		native, err := Asset{ID: "XLM"}.ContractID(types.PUBLIC)
		require.NoError(t, err)
		rpc := &fakeDecimalsRPC{}
		paths := []types.SwapPath{{Share: "1", Hops: []types.SwapHop{
			{Venue: "Soroswap", From: usdcContract, To: native},
			{Venue: "Soroswap", From: native, To: "BTKN"},
		}}}
		NewLifiSource(LifiConfig{RPC: rpc}, nil).labelSACHops(context.Background(), types.PUBLIC, Asset{ID: usdcContract, Contract: usdcContract}, labelTo, paths)
		assert.Equal(t, "XLM", paths[0].Hops[0].To)
		assert.Equal(t, usdcContract, paths[0].Hops[0].From, "the sold token keeps the ID it was asked for")
		assert.Equal(t, 0, rpc.ledgerReads())
	})
	t.Run("a missing entry or failed read is asked again; an answer is cached", func(t *testing.T) {
		fail := true
		answer := instanceEntries(t, map[string]string{usdcContract: "USDC:" + usdcIssuer})
		var asked [][]string
		rpc := &fakeDecimalsRPC{ledger: func(keys []string) (map[string]string, error) {
			asked = append(asked, keys)
			if fail {
				return nil, errors.New("connection reset")
			}
			return answer(keys)
		}}
		src := NewLifiSource(LifiConfig{RPC: rpc}, nil)
		paths := middleHops()
		src.labelSACHops(context.Background(), types.PUBLIC, labelFrom, labelTo, paths)
		assert.Equal(t, usdcContract, paths[0].Hops[1].To)

		fail = false
		for range 2 {
			paths = middleHops()
			src.labelSACHops(context.Background(), types.PUBLIC, labelFrom, labelTo, paths)
			assert.Equal(t, "USDC:"+usdcIssuer, paths[0].Hops[1].To)
		}
		require.Len(t, asked, 3, "the failed read, the answer, then EURC again (it had no entry)")
		assert.Len(t, asked[2], 1, "USDC came from the cache")
	})
}

func TestLifiLabelsCountResults(t *testing.T) {
	m := metrics.NewSwap(prometheus.NewRegistry())
	rpc := &fakeDecimalsRPC{ledger: instanceEntries(t, map[string]string{
		usdcContract: "USDC:" + usdcIssuer,
		eurcContract: "USDC:" + usdcIssuer, // an instance that does not derive back
	})}
	src := NewLifiSource(LifiConfig{RPC: rpc}, m)
	src.labelSACHops(context.Background(), types.PUBLIC, labelFrom, labelTo, middleHops())
	assert.Equal(t, 1.0, testutil.ToFloat64(m.HopLabels.WithLabelValues("labeled")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.HopLabels.WithLabelValues("not_sac")))

	rpc.ledger = func([]string) (map[string]string, error) { return nil, errors.New("down") }
	other := NewLifiSource(LifiConfig{RPC: rpc}, m)
	other.labelSACHops(context.Background(), types.PUBLIC, labelFrom, labelTo, middleHops())
	assert.Equal(t, 2.0, testutil.ToFloat64(m.HopLabels.WithLabelValues("unresolved")))
}

func TestLifiQuoteLabelsMiddleSACs(t *testing.T) {
	body := lifiFixture(t, "lifi_quote_xlm_eurc_split.json")
	var q LifiQuote
	require.NoError(t, json.Unmarshal(body, &q))
	rpc := &fakeDecimalsRPC{fn: decimalsOf(7), ledger: instanceEntries(t, map[string]string{usdcContract: "USDC:" + usdcIssuer})}
	req := lifiReq(fmtRaw(t, q.Action.FromAmount, 7))
	req.ToAsset = eurcContract

	rt, err := lifiServerRPC(t, 200, body, rpc).Quote(context.Background(), req)
	require.NoError(t, err)
	tokens := hopTokens(rt.Paths)
	assert.Contains(t, tokens, "USDC:"+usdcIssuer)
	assert.NotContains(t, tokens, usdcContract)
	assert.Contains(t, tokens, eurcContract, "the bought token keeps the ID it was asked for")
}

// blockingLedgerRPC never answers GetLedgerEntriesXDR; it returns only when ctx ends.
type blockingLedgerRPC struct{ utils.MockRPCService }

func (*blockingLedgerRPC) GetLedgerEntriesXDR(ctx context.Context, _ []string, _ string) (map[string]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestLifiLabelsFinishBeforeTheQuoteDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	paths := middleHops()
	NewLifiSource(LifiConfig{RPC: &blockingLedgerRPC{}}, nil).labelSACHops(ctx, types.PUBLIC, labelFrom, labelTo, paths)
	require.NoError(t, ctx.Err(), "labelling ran into the quote's deadline, so the router would drop the route")
	assert.Equal(t, usdcContract, paths[0].Hops[1].To)
}
