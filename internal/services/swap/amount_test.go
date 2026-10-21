package swap

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnits(t *testing.T) {
	t.Parallel()
	v, err := ParseUnits("100.5", 7)
	require.NoError(t, err)
	assert.Equal(t, "1005000000", v.String())

	v, err = ParseUnits("1", 18)
	require.NoError(t, err)
	assert.Equal(t, "1000000000000000000", v.String())

	for _, bad := range []string{"", "abc", "-1", "0", "0.0", "1.12345678", "1e5", " 1"} {
		_, err := ParseUnits(bad, 7)
		assert.Error(t, err, bad)
	}
}

func TestFormatUnits(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "17545.0814603", FormatUnits(big.NewInt(175450814603), 7))
	assert.Equal(t, "250", FormatUnits(big.NewInt(2500000000), 7))
	assert.Equal(t, "0.0000001", FormatUnits(big.NewInt(1), 7))
}
