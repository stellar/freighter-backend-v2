package auth

import "context"

// contextKey is an unexported type so context values keyed by it cannot collide
// with keys from other packages. The name field keeps distinct keys distinct
// (and aids debugging) if more are added to this package later.
type contextKey struct{ name string }

var userIDKey = contextKey{name: "userID"}

// ContextWithUserID returns a child context carrying the authenticated user ID
// (the hex-encoded auth public key from the JWT `sub` claim).
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext returns the authenticated user ID and whether one was set.
// Anonymous requests (permissive mode, no token) return ("", false).
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok
}
