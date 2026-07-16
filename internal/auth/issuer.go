package auth

import (
	"net/http"
	"strings"

	jwtgo "github.com/golang-jwt/jwt/v5"
)

// IssuerFromRequestUnverified returns the `iss` claim of the request's bearer
// token parsed WITHOUT signature or timing verification, or "" if there is no
// bearer token or it cannot be parsed.
//
// It reads only the Authorization header (never the body). The result is
// UNVERIFIED and client-spoofable: use it ONLY for a bounded, best-effort
// observability label (metric client bucket, log field) on the rejection path —
// never for an authentication or authorization decision.
func IssuerFromRequestUnverified(r *http.Request) string {
	scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	var claims jwtgo.RegisteredClaims
	parser := jwtgo.NewParser()
	if _, _, err := parser.ParseUnverified(token, &claims); err != nil {
		return ""
	}
	return claims.Issuer
}
