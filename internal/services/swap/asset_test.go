package swap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stellar/freighter-backend-v2/internal/types"
)

const usdcIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

func TestParseAsset(t *testing.T) {
	t.Parallel()
	a, err := ParseAsset("XLM")
	require.NoError(t, err)
	assert.True(t, a.IsNative())
	assert.True(t, a.IsClassic())

	a, err = ParseAsset("USDC:" + usdcIssuer)
	require.NoError(t, err)
	assert.True(t, a.IsClassic())

	a, err = ParseAsset("CBI7UCH5KGSVQRO5H4SUCZUTZABCITZLRHQQZTWL2TK4RZ72TAR6IHRV")
	require.NoError(t, err)
	assert.False(t, a.IsClassic())

	for _, bad := range []string{
		"", "native", "NATIVE", "xlm", "USDC", "USDC:GBAD", "TOOLONGCODE123:" + usdcIssuer, "CBAD",
		"US-DC:" + usdcIssuer, "ÜSDC:" + usdcIssuer, // code must be ASCII alphanumeric
		"USDC:" + usdcIssuer + ",EURC:" + usdcIssuer,                                 // one asset only
		"USDC:MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAACJUQ", // muxed issuer
	} {
		_, err := ParseAsset(bad)
		assert.Error(t, err, bad)
	}
}

func TestContractIDMatchesKnownSACs(t *testing.T) {
	t.Parallel()
	xlm, _ := ParseAsset("XLM")
	id, err := xlm.ContractID(types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA", id)

	usdc, _ := ParseAsset("USDC:" + usdcIssuer)
	id, err = usdc.ContractID(types.PUBLIC)
	require.NoError(t, err)
	assert.Equal(t, "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75", id)
}
