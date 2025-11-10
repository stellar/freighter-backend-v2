package middleware

import (
	"net/http"
)

func ResponseHeader() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			// Handle preflight requests (OPTIONS method)
    		if r.Method == http.MethodOptions {
    			w.WriteHeader(http.StatusOK)
    			return
    		}
			next.ServeHTTP(w, r)
		})
	}
}
