package assetid

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

// A real SEP-41 contract id (SolvBTC on pubnet, Appendix A.7).
const validContract = "CBIJBDNZNF4X35BJ4FFZWCDBSCKOP5NB4PLG4SNENRMLAPYG4P5FM6VN"

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

		// SEP-41 contract tokens: the canonical (and upstream) form is the
		// bare contract id. Clients also send SYMBOL:CONTRACTID; the symbol
		// half is opaque contract metadata — no alphanumeric or length
		// validation — and is stripped.
		{"bare contract id", validContract, validContract, false},
		{"symbol:contract strips symbol", "SolvBTC:" + validContract, validContract, false},
		{"long symbol accepted", "averylongtokensymbolover12chars:" + validContract, validContract, false},
		{"non-alphanumeric symbol accepted", "Solv-BTC!.x:" + validContract, validContract, false},
		{"colon in symbol accepted", "Solv:BTC:" + validContract, validContract, false},
		{"contract id casing not normalized away", "xlm2:" + validContract, validContract, false},
		{"invalid contract id still malformed", "SolvBTC:CINVALIDCONTRACTIDXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX", "", true},
		{"bare C-prefixed junk rejected", "CNOTREAL", "", true},
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
		// The verified SEP-41 wire format is the raw contract id, verbatim
		// (Appendix A.7): colon-less input passes through unchanged.
		{"contract id passes through", validContract, validContract},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ToStellarExpert(tc.canonical))
		})
	}
}
