package utils

import (
	"net/http"
	"time"
)

// NewUpstreamHTTPClient is the client for every upstream HTTP dependency.
// Go's default transport keeps two idle connections per host and no cap on
// connections per host, so a burst re-dials and fans out; this pool does not.
// The timeout is a backstop: callers bound each request with a context.
func NewUpstreamHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			MaxConnsPerHost:       50,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}
