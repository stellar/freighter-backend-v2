// ABOUTME: Middleware that limits the size of request bodies to prevent DoS attacks.
// ABOUTME: Uses http.MaxBytesReader to enforce a configurable maximum body size.
package middleware

import (
	"errors"
	"net/http"
)

const (
	// DefaultMaxBodySize is the default maximum request body size (1MB)
	DefaultMaxBodySize = 1 << 20 // 1MB
)

// BodySizeLimit returns a middleware that limits the size of request bodies.
// If maxBytes is 0, DefaultMaxBodySize (1MB) is used.
func BodySizeLimit(maxBytes int64) Middleware {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodySize
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only limit body size for methods that have bodies
			if r.Body != nil && r.ContentLength != 0 {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IsMaxBytesError reports whether err (or anything it wraps) is the error
// http.MaxBytesReader returns when the body exceeds the limit. It uses errors.As
// so it still matches when the error has been wrapped with %w (e.g. the auth
// verifier wraps the io.ReadAll failure with request context).
func IsMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}
