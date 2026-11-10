package swap

import (
	"context"
	"time"

	"github.com/stellar/go-stellar-sdk/ingest/sac"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// labelBudget caps what labelling may add to a quote, and labelling ends
// labelMargin before the quote's own deadline: the router drops a source that
// reaches its deadline, and an unlabelled hop still shows.
const (
	labelBudget = time.Second
	labelMargin = 250 * time.Millisecond
)

// labelSACHops rewrites each hop token in the middle of a route that is a
// Stellar Asset Contract to its asset ID (CODE:ISSUER or XLM), which clients
// can name and find an icon for. The sold and bought tokens keep the IDs the
// client asked for. A SAC's instance entry holds its asset, and
// sac.AssetFromContractData accepts it only when that asset derives back to
// the contract ID, so no contract can pass itself off as another asset.
func (s *LifiSource) labelSACHops(ctx context.Context, net string, from, to Asset, paths []types.SwapPath) {
	native, err := Asset{ID: "XLM"}.ContractID(net)
	if err != nil {
		return
	}
	pass, err := Passphrase(net)
	if err != nil {
		return
	}
	labels := map[string]string{}
	keyOf := map[string]string{}
	var keys []string
	for _, p := range paths {
		for _, h := range p.Hops {
			for _, t := range []string{h.From, h.To} {
				if _, seen := labels[t]; seen || keyOf[t] != "" || t == from.ID || t == to.ID || !strkey.IsValidContractAddress(t) {
					continue
				}
				if t == native {
					labels[t] = "XLM"
					metrics.RecordSwapHopLabel(s.m, "labeled")
					continue
				}
				if v, ok := s.labels.Load(t); ok {
					labels[t] = v.(string)
					metrics.RecordSwapHopLabel(s.m, labelResult(labels[t]))
					continue
				}
				k, err := instanceKey(t)
				if err != nil {
					continue
				}
				keyOf[t] = k
				keys = append(keys, k)
			}
		}
	}
	if len(keys) > 0 {
		s.readSACLabels(ctx, net, pass, keyOf, keys, labels)
	}
	for i := range paths {
		for j := range paths[i].Hops {
			h := &paths[i].Hops[j]
			if l := labels[h.From]; l != "" {
				h.From = l
			}
			if l := labels[h.To]; l != "" {
				h.To = l
			}
		}
	}
}

// readSACLabels reads every pending instance entry in one call. An answer,
// SAC or not, is cached for the process: a contract can't become or stop
// being a SAC. A failed read or a missing entry (archived, or a ledger the
// node hasn't reached) is not cached.
func (s *LifiSource) readSACLabels(ctx context.Context, net, pass string, keyOf map[string]string, keys []string, labels map[string]string) {
	deadline := time.Now().Add(labelBudget)
	if d, ok := ctx.Deadline(); ok && d.Add(-labelMargin).Before(deadline) {
		deadline = d.Add(-labelMargin)
	}
	unresolved := func(n int) {
		for range n {
			metrics.RecordSwapHopLabel(s.m, "unresolved")
		}
	}
	if !time.Now().Before(deadline) {
		unresolved(len(keys))
		return
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	entries, err := s.cfg.RPC.GetLedgerEntriesXDR(ctx, keys, net)
	if err != nil {
		unresolved(len(keys))
		return
	}
	for contract, k := range keyOf {
		b64, ok := entries[k]
		if !ok {
			unresolved(1)
			continue
		}
		var data xdr.LedgerEntryData
		if err := xdr.SafeUnmarshalBase64(b64, &data); err != nil {
			unresolved(1)
			continue
		}
		label := ""
		if asset, ok := sac.AssetFromContractData(xdr.LedgerEntry{Data: data}, pass); ok {
			label = asset.StringCanonical()
		}
		s.labels.Store(contract, label)
		labels[contract] = label
		metrics.RecordSwapHopLabel(s.m, labelResult(label))
	}
}

func labelResult(label string) string {
	if label == "" {
		return "not_sac"
	}
	return "labeled"
}
