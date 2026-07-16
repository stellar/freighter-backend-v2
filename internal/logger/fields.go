package logger

import (
	"context"
	"sync"
)

// Fields is a request-scoped, mutable set of structured log key/value pairs.
// The Logging middleware seeds one per request into the context; downstream
// middleware (e.g. Auth) enrich it, and Logging appends the collected pairs to
// its single per-request log line. All methods are nil-safe so callers can use
// FieldsFromContext(ctx).Set(...) without a presence check.
type Fields struct {
	mu   sync.Mutex
	args []any
}

// NewFields returns an empty holder.
func NewFields() *Fields { return &Fields{} }

// Set appends a key/value pair. Callers set a given key at most once per request.
func (f *Fields) Set(key string, value any) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.args = append(f.args, key, value)
}

// Args returns a copy of the collected key/value pairs, suitable for splatting
// into a slog variadic call.
func (f *Fields) Args() []any {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]any, len(f.args))
	copy(out, f.args)
	return out
}

type fieldsCtxKey struct{}

// ContextWithFields returns a child context carrying f.
func ContextWithFields(ctx context.Context, f *Fields) context.Context {
	return context.WithValue(ctx, fieldsCtxKey{}, f)
}

// FieldsFromContext returns the holder carried by ctx, or nil if none was
// seeded. The nil result is safe to call Set/Args on.
func FieldsFromContext(ctx context.Context) *Fields {
	f, _ := ctx.Value(fieldsCtxKey{}).(*Fields)
	return f
}
