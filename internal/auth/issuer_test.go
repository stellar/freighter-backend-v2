package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIssuerFromRequestUnverified(t *testing.T) {
	_, priv, sub := newKeypair(t)
	_, otherPriv, _ := newKeypair(t)
	validToken := mint(t, priv, validClaims(sub, testMethodAndPath, nil))
	// A token signed by the wrong key still PARSES; iss is readable though the
	// signature would fail verification.
	wrongKeyToken := mint(t, otherPriv, validClaims(sub, testMethodAndPath, nil))

	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"valid token", "Bearer " + validToken, "freighter-extension"},
		{"bad-signature token (still parseable)", "Bearer " + wrongKeyToken, "freighter-extension"},
		{"malformed token", "Bearer not-a-jwt", ""},
		{"no header", "", ""},
		{"non-bearer scheme", "Basic abc123", ""},
		{"empty bearer", "Bearer ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			assert.Equal(t, tc.want, IssuerFromRequestUnverified(r))
		})
	}
}
