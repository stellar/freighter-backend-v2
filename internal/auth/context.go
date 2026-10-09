package auth

import (
	"context"
	"time"
)

// contextKey is an unexported type so context values keyed by it cannot collide
// with keys from other packages. The name field keeps distinct keys distinct
// (and aids debugging).
type contextKey struct{ name string }

var (
	sourceIDKey = contextKey{name: "sourceID"}
	issuedAtKey = contextKey{name: "issuedAt"}
)

// ContextWithSourceID returns a child context carrying the authenticated SOURCE
// id: the hex-encoded auth public key from the JWT `sub` claim.
//
// A source id is not a user id. Under the Unified User Model v2 a user owns one
// or more sources (every seed phrase and account key they have proven possession
// of), and resolving a source to its user is a database lookup. The middleware
// deliberately does not perform it, so this context carries only what the
// signature proved. Handlers that need a user call users.ResolveUser with this
// value; handlers that don't never touch the database.
func ContextWithSourceID(ctx context.Context, sourceID string) context.Context {
	return context.WithValue(ctx, sourceIDKey, sourceID)
}

// SourceIDFromContext returns the authenticated source id and whether one was
// set. Anonymous requests (permissive mode, no token) return ("", false).
func SourceIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sourceIDKey).(string)
	return id, ok
}

// ContextWithIssuedAt returns a child context carrying the token's
// SIGNATURE-VERIFIED `iat` claim. It is set beside the source id on every
// authenticated request so handlers that sign or verify consents over the
// token's issue time (the user-link handler) read it from here and never from
// the request body, where it would be unauthenticated.
func ContextWithIssuedAt(ctx context.Context, issuedAt time.Time) context.Context {
	return context.WithValue(ctx, issuedAtKey, issuedAt)
}

// IssuedAtFromContext returns the verified `iat` of the request's token and
// whether one was set. It is set exactly when a source id is set.
func IssuedAtFromContext(ctx context.Context) (time.Time, bool) {
	iat, ok := ctx.Value(issuedAtKey).(time.Time)
	return iat, ok
}
