package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

// Shaped after the measured XLM /asset payload (Appendix A.2). Supply must
// survive as an exact integer string — it exceeds float64's 2^53 exact range.
func TestStellarExpertAsset_DecodesStatsFields(t *testing.T) {
	t.Parallel()

	payload := `{
		"price": 0.1604,
		"supply": 1054439020873472865,
		"volume7d": 103319258398384,
		"trustlines": {"total": 10000000, "funded": 9926520},
		"created": 0
	}`
	var asset types.StellarExpertAsset
	require.NoError(t, json.Unmarshal([]byte(payload), &asset))

	assert.Equal(t, 0.1604, asset.Price)
	assert.Equal(t, "1054439020873472865", asset.Supply.String(), "supply must not round-trip through float64")
	assert.Nil(t, asset.Decimals, "absent decimals stays nil (callers default to 7)")
	assert.Equal(t, float64(103319258398384), asset.Volume7d)
	require.NotNil(t, asset.Trustlines.Funded)
	assert.Equal(t, int64(9926520), *asset.Trustlines.Funded)
	assert.Equal(t, int64(0), asset.Created)
}

// USDC-shaped: explicit decimals, absent trustlines.funded stays nil.
func TestStellarExpertAsset_AbsentFieldsStayNil(t *testing.T) {
	t.Parallel()

	payload := `{"price": 1.000007, "supply": 4349718972397050, "decimals": 7}`
	var asset types.StellarExpertAsset
	require.NoError(t, json.Unmarshal([]byte(payload), &asset))

	require.NotNil(t, asset.Decimals)
	assert.Equal(t, 7, *asset.Decimals)
	assert.Nil(t, asset.Trustlines.Funded)
	assert.Equal(t, "4349718972397050", asset.Supply.String())

	var empty types.StellarExpertAsset
	require.NoError(t, json.Unmarshal([]byte(`{"price": 0.5}`), &empty))
	assert.Equal(t, "", empty.Supply.String(), "absent supply decodes to the empty json.Number")
	assert.Nil(t, empty.Trustlines.Funded)
}

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

// A stats field whose upstream shape drifts must be dropped, never fail the
// whole payload. StellarExpertAsset is shared with /token-prices, which
// needs only `price`: while every field decoded strictly, a `trustlines`
// array (upstream has shipped both an object and an array for that key)
// failed the decode and nulled the price for EVERY token on the home
// screen — a field none of those callers even read.
func TestStellarExpertAsset_StatsShapeDriftKeepsPrice(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"supply as a quoted number", `{"price":0.16,"supply":"1054439020873472865"}`},
		{"supply as an object", `{"price":0.16,"supply":{"amount":1}}`},
		{"decimals as a float", `{"price":0.16,"decimals":7.0}`},
		{"volume7d as a quoted number", `{"price":0.16,"volume7d":"103319258398384"}`},
		{"trustlines as an array", `{"price":0.16,"trustlines":[{"funded":9926520}]}`},
		{"trustlines.funded as a string", `{"price":0.16,"trustlines":{"funded":"9926520"}}`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var asset types.StellarExpertAsset
			require.NoError(t, json.Unmarshal([]byte(tc.payload), &asset),
				"a drifted stats field must be dropped, not fail the payload")
			assert.Equal(t, 0.16, asset.Price, "price must still decode")
		})
	}
}

// Tolerance is per field: one drifted stats field must not take the others
// with it, or /token-stats degrades to an empty section on any drift.
func TestStellarExpertAsset_DriftIsIsolatedToTheDriftedField(t *testing.T) {
	t.Parallel()

	payload := `{
		"price": 0.1604,
		"supply": "not-a-number",
		"decimals": 7,
		"volume7d": 103319258398384,
		"trustlines": {"funded": 9926520}
	}`
	var asset types.StellarExpertAsset
	require.NoError(t, json.Unmarshal([]byte(payload), &asset))

	assert.Equal(t, "", asset.Supply.String(), "the drifted field is dropped")
	assert.Equal(t, 0.1604, asset.Price)
	require.NotNil(t, asset.Decimals)
	assert.Equal(t, 7, *asset.Decimals)
	assert.Equal(t, float64(103319258398384), asset.Volume7d)
	require.NotNil(t, asset.Trustlines.Funded)
	assert.Equal(t, int64(9926520), *asset.Trustlines.Funded)
}

// `price` stays strict. It is the only field /token-prices reads, so a shape
// change there is a real failure that must surface as an error and go
// uncached, not be silently flattened to 0 and negative-cached as unpriced.
func TestStellarExpertAsset_PriceDriftStillFails(t *testing.T) {
	t.Parallel()

	var asset types.StellarExpertAsset
	assert.Error(t, json.Unmarshal([]byte(`{"price":"0.16"}`), &asset))
}
