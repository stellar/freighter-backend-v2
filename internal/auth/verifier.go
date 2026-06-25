package auth

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTPRequestVerifier verifies the JWT carried by an HTTP request and returns the
// authenticated user ID (hex-encoded auth public key).
type HTTPRequestVerifier interface {
	VerifyHTTPRequest(r *http.Request) (userID string, err error)
}

// Verifier is the default HTTPRequestVerifier. It holds no key material — the
// verification key comes from each token's subject — and no body-size limit:
// request bodies are bounded upstream by the BodySizeLimit middleware
// (http.MaxBytesReader), so the verifier reads them in full rather than imposing
// a second, divergent cap that could silently truncate the body it hashes.
type Verifier struct{}

// NewVerifier returns a Verifier.
func NewVerifier() *Verifier {
	return &Verifier{}
}

// VerifyHTTPRequest extracts the bearer token, binds it to the request's
// method+path and body, and verifies it. It returns ErrNoToken when no bearer
// token is present (so callers can allow anonymous access in permissive mode);
// any other error indicates a present-but-invalid token.
func (v *Verifier) VerifyHTTPRequest(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrNoToken
	}
	// RFC 6750: the "Bearer" auth scheme is case-insensitive.
	scheme, token, _ := strings.Cut(authHeader, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		// A different (or unparseable) auth scheme — not a bearer token this
		// verifier handles, so treat it as no token (anonymous in permissive mode).
		return "", ErrNoToken
	}
	// A Bearer scheme with an empty or whitespace-only credential is a
	// present-but-invalid token, not "no token": reject it (401 in both modes)
	// rather than letting permissive mode pass it through as anonymous.
	token = strings.TrimSpace(token)
	if token == "" {
		return "", &VerificationError{Reason: ReasonMalformed, Err: errors.New("empty bearer credential")}
	}

	body, err := readAndResetBody(r)
	if err != nil {
		// Operational failure, not an auth failure: don't wrap ErrUnauthorized.
		return "", fmt.Errorf("reading request body: %w", err)
	}

	methodAndPath := fmt.Sprintf("%s %s", r.Method, r.URL.RequestURI())
	claims, err := ParseJWT(token, methodAndPath, body)
	if err != nil {
		return "", err
	}
	return claims.Subject, nil
}

// readAndResetBody reads the full body (already bounded upstream by
// http.MaxBytesReader) for hashing, then restores r.Body so downstream handlers
// can read it again.
func readAndResetBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

var _ HTTPRequestVerifier = (*Verifier)(nil)
