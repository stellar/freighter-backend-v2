package auth

import "context"

type contextKey struct{}

var userIDKey = contextKey{}

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
