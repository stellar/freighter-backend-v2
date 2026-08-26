package types_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// The measured wire order is [ts, open, high, low, close, quote_volume,
// base_volume, trades]: index 2 >= index 3 in 96/96 sampled candles for both
// XLM and USDC (the upstream docs reverse high and low). Only TS, Open, and
// Close have accessors; High()/Low() are deliberately not exposed until
// something needs them, so nobody can wire them backwards from the old
// comment.
func TestStellarExpertCandle_Accessors(t *testing.T) {
	t.Parallel()

	candle := types.StellarExpertCandle{
		1786636800, // ts
		0.158900,   // open
		0.159039,   // high
		0.158807,   // low
		0.158955,   // close
		101.5,      // quote_volume
		638.2,      // base_volume
		42,         // trades
	}

	assert.Equal(t, int64(1786636800), candle.TS())
	assert.Equal(t, 0.158900, candle.Open())
	assert.Equal(t, 0.158955, candle.Close())
}
