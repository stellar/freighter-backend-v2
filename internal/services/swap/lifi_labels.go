package swap

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"

	"github.com/stellar/freighter-backend-v2/internal/metrics"
	"github.com/stellar/freighter-backend-v2/internal/types"
	"github.com/stellar/freighter-backend-v2/internal/utils"
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
// client asked for.
func (s *LifiSource) labelSACHops(ctx context.Context, account, net string, from, to Asset, paths []types.SwapPath) {
	var ids []string
	seen := map[string]bool{}
	for _, p := range paths {
		for _, h := range p.Hops {
			for _, t := range []string{h.From, h.To} {
				if !seen[t] && t != from.ID && t != to.ID && strkey.IsValidContractAddress(t) {
					seen[t] = true
					ids = append(ids, t)
				}
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	deadline := time.Now().Add(labelBudget)
	if d, ok := ctx.Deadline(); ok && d.Add(-labelMargin).Before(deadline) {
		deadline = d.Add(-labelMargin)
	}
	if !time.Now().Before(deadline) {
		return
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	labels := make([]string, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			labels[i] = s.sacLabel(ctx, account, net, id)
		}()
	}
	wg.Wait()
	byID := make(map[string]string, len(ids))
	for i, id := range ids {
		if labels[i] != "" {
			byID[id] = labels[i]
		}
	}
	for i := range paths {
		for j := range paths[i].Hops {
			h := &paths[i].Hops[j]
			if l, ok := byID[h.From]; ok {
				h.From = l
			}
			if l, ok := byID[h.To]; ok {
				h.To = l
			}
		}
	}
}

// sacLabel returns the asset ID of the SAC at contract, or "". A SAC's name()
// is CODE:ISSUER or "native"; any contract can return that string, so it is
// trusted only when the asset derives back to this contract ID.
func (s *LifiSource) sacLabel(ctx context.Context, account, net, contract string) string {
	if v, ok := s.labels.Load(contract); ok {
		return v.(string)
	}
	addr, err := utils.ScAddressFromString(contract)
	if err != nil {
		return ""
	}
	res, err := s.cfg.RPC.SimulateInvocation(ctx, *addr, &txnbuild.SimpleAccount{AccountID: account}, "name", nil, txnbuild.NewTimeout(300), net)
	if err != nil {
		// A failed RPC call says nothing about the contract: not cached.
		var ue *metrics.UpstreamError
		if errors.As(err, &ue) && ue.Kind == "simulation_error" {
			s.labels.Store(contract, "")
		}
		return ""
	}
	label := ""
	if res == nil {
		return ""
	}
	if str, ok := res.GetStr(); ok {
		name := string(str)
		if name == "native" {
			name = "XLM"
		}
		if a, err := ParseAsset(name); err == nil && a.IsClassic() {
			if id, err := a.ContractID(net); err == nil && id == contract {
				label = a.ID
			}
		}
	}
	s.labels.Store(contract, label)
	return label
}
