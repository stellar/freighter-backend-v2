package assetid

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

func TestNormalize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"XLM upper", "XLM", "XLM", false},
		{"xlm lower", "xlm", "XLM", false},
		{"native lower", "native", "XLM", false},
		{"NATIVE upper", "NATIVE", "XLM", false},
		{"4-char code", "USDC:" + validIssuer, "USDC:" + validIssuer, false},
		{"1-char code", "X:" + validIssuer, "X:" + validIssuer, false},
		{"5-char code", "yXLM2:" + validIssuer, "yXLM2:" + validIssuer, false},
		{"12-char code", "ABCDEFGHIJKL:" + validIssuer, "ABCDEFGHIJKL:" + validIssuer, false},
		{"13-char code rejected", "ABCDEFGHIJKLM:" + validIssuer, "", true},
		{"empty code rejected", ":" + validIssuer, "", true},
		{"non-alphanumeric code rejected", "USD-C:" + validIssuer, "", true},
		{"missing issuer", "USDC:", "", true},
		{"too many colons", "USDC:" + validIssuer + ":foo", "", true},
		{"malformed issuer", "USDC:NOT-A-STELLAR-KEY", "", true},
		{"surrounding whitespace trimmed", "  XLM ", "XLM", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Normalize(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestToStellarExpert(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		canonical string
		want      string
	}{
		{"native", "XLM", "XLM"},
		{"4-char code uses type 1", "USDC:" + validIssuer, "USDC-" + validIssuer + "-1"},
		{"1-char code uses type 1", "X:" + validIssuer, "X-" + validIssuer + "-1"},
		{"5-char code uses type 2", "yXLM2:" + validIssuer, "yXLM2-" + validIssuer + "-2"},
		{"12-char code uses type 2", "ABCDEFGHIJKL:" + validIssuer, "ABCDEFGHIJKL-" + validIssuer + "-2"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ToStellarExpert(tc.canonical))
		})
	}
}
