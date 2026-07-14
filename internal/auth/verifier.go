package auth

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Identity is the authenticated principal extracted from a verified request JWT.
type Identity struct {
	UserID string // hex-encoded auth public key (the JWT `sub`); also the user ID
	Issuer string // client type (the JWT `iss`), e.g. "freighter-extension"; trusted (from a signature-verified token)
}

// HTTPRequestVerifier verifies the JWT carried by an HTTP request and returns the
// authenticated user ID (hex-encoded auth public key).
type HTTPRequestVerifier interface {
	VerifyHTTPRequest(r *http.Request) (Identity, error)
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
func (v *Verifier) VerifyHTTPRequest(r *http.Request) (Identity, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return Identity{}, ErrNoToken
	}
	// RFC 6750: the "Bearer" auth scheme is case-insensitive.
	scheme, token, _ := strings.Cut(authHeader, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		// A different (or unparseable) auth scheme — not a bearer token this
		// verifier handles, so treat it as no token (anonymous in permissive mode).
		return Identity{}, ErrNoToken
	}
	// A Bearer scheme with an empty or whitespace-only credential is a
	// present-but-invalid token, not "no token": reject it (401 in both modes)
	// rather than letting permissive mode pass it through as anonymous.
	token = strings.TrimSpace(token)
	if token == "" {
		return Identity{}, &VerificationError{Reason: ReasonMalformed, Err: errors.New("empty bearer credential")}
	}

	body, err := readAndResetBody(r)
	if err != nil {
		// Operational failure, not an auth failure: don't wrap ErrUnauthorized.
		return Identity{}, fmt.Errorf("reading request body: %w", err)
	}

	methodAndPath := fmt.Sprintf("%s %s", r.Method, r.URL.RequestURI())
	claims, err := ParseJWT(token, methodAndPath, body)
	if err != nil {
		return Identity{}, err
	}
	return Identity{UserID: claims.Subject, Issuer: claims.Issuer}, nil
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
