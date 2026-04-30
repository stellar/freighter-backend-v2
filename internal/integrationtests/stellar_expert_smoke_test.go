package integrationtests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/services"
	"github.com/stellar/freighter-backend-v2/internal/types"
)

// TestStellarExpertSmoke exercises the real Stellar Expert /asset endpoint to
// verify the wire format used by the prices service has not drifted upstream.
// Network-only; no containers required. Gated to avoid charging CI for an
// external request on every push.
func TestStellarExpertSmoke(t *testing.T) {
	if os.Getenv("RUN_STELLAR_EXPERT_SMOKE") != "1" {
		t.Skip("set RUN_STELLAR_EXPERT_SMOKE=1 to run the Stellar Expert smoke test")
	}

	svc := services.NewStellarExpertService(
		"https://api.stellar.expert/explorer/public",
		"https://api.stellar.expert/explorer/testnet",
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("native XLM", func(t *testing.T) {
		asset, err := svc.GetAsset(ctx, types.PUBLIC, "XLM")
		require.NoError(t, err)
		require.NotNil(t, asset)
		assert.Greater(t, asset.Price, 0.0, "XLM price should be positive")
		assert.GreaterOrEqual(t, len(asset.Price7d), 2, "expected at least 2 daily candles for 24h delta")
	})

	t.Run("classic USDC", func(t *testing.T) {
		asset, err := svc.GetAsset(ctx, types.PUBLIC, "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN-1")
		require.NoError(t, err)
		require.NotNil(t, asset)
		assert.Greater(t, asset.Price, 0.0, "USDC price should be positive")
	})
}
