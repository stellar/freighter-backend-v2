package middleware

import (
	"net/http"
)

// Middleware defines a function that wraps an http.Handler with additional functionality
type Middleware func(http.Handler) http.Handler

// Chain applies multiple middleware to a handler in the order they are provided
func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	// Apply middleware in reverse order so they execute in the order they are provided.
	// If we want A(B(C(handler))) we need to apply them in reverse order, so that
	// the execution order is C(handler) -> B(C(handler)) -> A(B(C(handler))).
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
