package swap

import (
	"math/big"
	"strings"
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

// The cap bounds big.Int.SetString, which is superlinear in digits, before
// any range check runs; a 1 MiB body of digits otherwise costs ~0.5 s of CPU.
func TestParseUnitsCapsLength(t *testing.T) {
	t.Parallel()
	longest := strings.Repeat("9", maxAmountLen)
	v, err := ParseUnits(longest, 0)
	require.NoError(t, err)
	assert.Equal(t, longest, v.String())

	_, err = ParseUnits(longest+"9", 0)
	assert.ErrorIs(t, err, errBadAmount)
	_, err = ParseUnits(strings.Repeat("1", 1<<20), 0)
	assert.ErrorIs(t, err, errBadAmount)
}

func TestFormatUnits(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "17545.0814603", FormatUnits(big.NewInt(175450814603), 7))
	assert.Equal(t, "250", FormatUnits(big.NewInt(2500000000), 7))
	assert.Equal(t, "0.0000001", FormatUnits(big.NewInt(1), 7))
}
