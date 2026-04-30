package assetid

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stellar/go/strkey"
)

const (
	NativeCanonical = "XLM"

	maxCodeLen        = 12
	credit4MaxCodeLen = 4
)

var (
	ErrEmpty     = errors.New("token id is empty")
	ErrMalformed = errors.New("token id is malformed: expected \"XLM\" or \"CODE:ISSUER\"")
)

// Normalize accepts a client-side token identifier and returns its canonical
// form. "XLM", "xlm", and "native" all collapse to "XLM". A "CODE:ISSUER"
// pair is validated (1-12 alphanumeric code, valid ed25519 public key issuer)
// and returned with the code preserved as supplied.
func Normalize(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", ErrEmpty
	}

	if strings.EqualFold(trimmed, "XLM") || strings.EqualFold(trimmed, "native") {
		return NativeCanonical, nil
	}

	parts := strings.Split(trimmed, ":")
	if len(parts) != 2 {
		return "", ErrMalformed
	}

	code, issuer := parts[0], parts[1]
	if !isValidAssetCode(code) {
		return "", fmt.Errorf("%w: invalid asset code %q", ErrMalformed, code)
	}
	if _, err := strkey.Decode(strkey.VersionByteAccountID, issuer); err != nil {
		return "", fmt.Errorf("%w: invalid issuer %q", ErrMalformed, issuer)
	}

	return code + ":" + issuer, nil
}

// ToStellarExpert formats a canonical token id for the Stellar Expert
// /asset/{id} endpoint. Native maps to "XLM"; classic assets become
// "CODE-ISSUER-{1|2}" where the trailing type byte is derived from code length
// (1-4 → 1 / credit_alphanum4, 5-12 → 2 / credit_alphanum12).
func ToStellarExpert(canonical string) string {
	if canonical == NativeCanonical {
		return NativeCanonical
	}
	idx := strings.Index(canonical, ":")
	if idx <= 0 {
		return canonical
	}
	code, issuer := canonical[:idx], canonical[idx+1:]
	assetType := 2
	if len(code) <= credit4MaxCodeLen {
		assetType = 1
	}
	return fmt.Sprintf("%s-%s-%d", code, issuer, assetType)
}

func isValidAssetCode(code string) bool {
	n := len(code)
	if n < 1 || n > maxCodeLen {
		return false
	}
	for _, r := range code {
		if !isAlphanumeric(r) {
			return false
		}
	}
	return true
}

func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
