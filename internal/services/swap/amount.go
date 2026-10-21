package swap

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
)

const ClassicDecimals = 7

var decimalRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

var errBadAmount = errors.New("invalid amount")

// ParseUnits converts a positive decimal string to integer units.
func ParseUnits(s string, decimals int) (*big.Int, error) {
	if !decimalRe.MatchString(s) {
		return nil, errBadAmount
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > decimals {
		return nil, errBadAmount
	}
	digits := whole + frac + strings.Repeat("0", decimals-len(frac))
	v, ok := new(big.Int).SetString(digits, 10)
	if !ok || v.Sign() <= 0 {
		return nil, errBadAmount
	}
	return v, nil
}

// FormatUnits converts integer units back to a decimal string.
func FormatUnits(v *big.Int, decimals int) string {
	s := v.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	whole, frac := s[:len(s)-decimals], strings.TrimRight(s[len(s)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}
