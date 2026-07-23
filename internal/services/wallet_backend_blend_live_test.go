// ABOUTME: Env-gated live smoke test for the Blend GraphQL client against a real
// ABOUTME: wallet-backend (skipped unless WB_LIVE_URL is set; safe no-op in CI).
package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveBlendQueries exercises the three Blend client methods against a
// live wallet-backend (e.g. a port-forwarded dev instance) and writes the
// decoded responses to WB_LIVE_OUT for inspection/fixtures. Skipped unless
// WB_LIVE_URL is set.
func TestLiveBlendQueries(t *testing.T) {
	url := os.Getenv("WB_LIVE_URL")
	if url == "" {
		t.Skip("WB_LIVE_URL not set; live test skipped")
	}
	key := os.Getenv("WB_LIVE_KEY")
	outDir := os.Getenv("WB_LIVE_OUT")

	svc, err := NewWalletBackendService("", url, "", key, 1, nil)
	if err != nil {
		t.Fatalf("constructing service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dump := func(name string, v any, err error) {
		if err != nil {
			t.Errorf("%s error: %v", name, err)
			return
		}
		blob, _ := json.MarshalIndent(v, "", "  ")
		t.Logf("%s: %d bytes", name, len(blob))
		if outDir != "" {
			_ = os.WriteFile(filepath.Join(outDir, name+".json"), blob, 0o644)
		}
	}

	pools, err := svc.GetBlendPools(ctx, "TESTNET")
	dump("blend_pools", pools, err)

	address := os.Getenv("WB_LIVE_ADDRESS")
	if address == "" {
		address = "GDW6QB3BFPQ3I4LH752JD2HYADFM2T4RVRCEUNCCH7MICWZR67NL5552"
	}
	positions, err := svc.GetBlendPositions(ctx, address, "TESTNET")
	dump("blend_positions", positions, err)
}
