// ABOUTME: Middleware that limits the size of request bodies to prevent DoS attacks.
// ABOUTME: Uses http.MaxBytesReader to enforce a configurable maximum body size.
package middleware

import (
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

// IsMaxBytesError checks if an error is from http.MaxBytesReader exceeding the limit.
// This can be used by handlers to return an appropriate error response.
func IsMaxBytesError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*http.MaxBytesError)
	return ok
}
